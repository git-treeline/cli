package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/git-treeline/cli/internal/runtimeowner"
	"github.com/git-treeline/cli/internal/supervisor"
	"github.com/google/uuid"
)

const maxRuntimeHandoffBytes = 64 * 1024

type runtimeHandoff struct {
	Schema   int                  `json:"schema"`
	Binding  runtimeowner.Binding `json:"binding"`
	Port     int                  `json:"port"`
	PostStop []string             `json:"post_stop"`
}

// captureRuntimeHandoff records selected raw commands in their execution order.
// Port interpolation happens only in memory when the captured command runs.
func captureRuntimeHandoff(binding runtimeowner.Binding, selected []startHookEntry, port int) (runtimeHandoff, error) {
	h := runtimeHandoff{Schema: 1, Binding: binding, Port: port, PostStop: []string{}}
	for i := len(selected) - 1; i >= 0; i-- {
		h.PostStop = append(h.PostStop, selected[i].Hook.PostStop...)
	}
	h.Binding.PostStopDigest = h.postStopDigest()
	raw, err := encodeRuntimeHandoff(h)
	if err != nil {
		return runtimeHandoff{}, err
	}
	return decodeRuntimeHandoff(raw)
}

func (h runtimeHandoff) postStopDigest() string {
	raw, _ := json.Marshal(struct {
		Port     int      `json:"port"`
		PostStop []string `json:"post_stop"`
	}{h.Port, h.PostStop})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (h runtimeHandoff) validate() error {
	if h.Schema != 1 || h.Port < 1 || h.Port > 65535 || h.PostStop == nil || len(h.PostStop) > 1024 || h.Binding.PostStopDigest != h.postStopDigest() {
		return errors.New("invalid runtime handoff")
	}
	for _, command := range h.PostStop {
		if !utf8.ValidString(command) || strings.ContainsRune(command, 0) {
			return errors.New("invalid runtime post-stop plan")
		}
	}
	return nil
}

// encodeRuntimeHandoff supplies the existing private bootstrap payload. This is
// not a receipt: it contains raw configuration and must not be made public.
func encodeRuntimeHandoff(h runtimeHandoff) ([]byte, error) {
	if err := h.validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(h)
	if err != nil || len(raw) > maxRuntimeHandoffBytes {
		return nil, errors.New("runtime handoff exceeds size limit")
	}
	return raw, nil
}

func decodeRuntimeHandoff(raw []byte) (runtimeHandoff, error) {
	if len(raw) > maxRuntimeHandoffBytes || !utf8.Valid(raw) {
		return runtimeHandoff{}, errors.New("invalid runtime handoff")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var h runtimeHandoff
	if err := decoder.Decode(&h); err != nil {
		return runtimeHandoff{}, errors.New("invalid runtime handoff")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return runtimeHandoff{}, errors.New("trailing runtime handoff data")
	}
	canonical, err := encodeRuntimeHandoff(h)
	if err != nil {
		return runtimeHandoff{}, err
	}
	// The private producer uses this same encoding. Exact bytes reject duplicate
	// and missing fields as well as noncanonical alternate representations.
	if !bytes.Equal(raw, canonical) {
		return runtimeHandoff{}, errors.New("noncanonical runtime handoff")
	}
	return h, nil
}

// configureTrackedRuntime installs completion only after the exact prepared
// binding and verified owner have been retained. The caller verifies the live
// target/allocation before this handoff; no PID discovery happens here.
func configureTrackedRuntime(sv *supervisor.Supervisor, store *runtimeowner.Store, h runtimeHandoff, verifiedOwner runtimeowner.Owner, run checkedHookRunner) error {
	if sv == nil || store == nil {
		return runtimeowner.ErrUnknown
	}
	raw, err := encodeRuntimeHandoff(h)
	if err != nil {
		return err
	}
	h, err = decodeRuntimeHandoff(raw)
	if err != nil {
		return err
	}
	if len(h.PostStop) > 0 && run == nil {
		return errors.New("checked runtime post-stop runner unavailable")
	}
	ctx := context.Background()
	record, err := store.Query(ctx, h.Binding)
	if err != nil {
		return err
	}
	if record.State != runtimeowner.Prepared && record.State != runtimeowner.Owned {
		return runtimeowner.ErrUncertain
	}
	record, err = store.RegisterOwner(ctx, h.Binding, verifiedOwner)
	if err != nil {
		return err
	}
	if record.State != runtimeowner.Owned {
		return runtimeowner.ErrUncertain
	}

	var mu sync.Mutex
	var operationID string
	var requested, claimed, attempted bool
	var completionErr error
	sv.RuntimeGeneration = h.Binding.Generation
	sv.ShutdownRequested = func(requestedOperation string) error {
		mu.Lock()
		defer mu.Unlock()
		if requested {
			return runtimeowner.ErrUncertain
		}
		requested = true
		operationID = requestedOperation
		if operationID == "" {
			operationID = uuid.NewString()
		}
		_, granted, beginErr := store.BeginStop(ctx, h.Binding, verifiedOwner, operationID)
		claimed = granted
		if beginErr != nil {
			return beginErr
		}
		if !claimed {
			return runtimeowner.ErrUncertain
		}
		return nil
	}
	sv.ShutdownComplete = func() error {
		mu.Lock()
		defer mu.Unlock()
		if !claimed {
			return runtimeowner.ErrUncertain
		}
		if attempted {
			return completionErr
		}
		attempted = true
		completionErr = runtimeowner.ErrUncertain
		current, err := store.Query(ctx, h.Binding)
		if err != nil {
			completionErr = err
			return completionErr
		}
		if current.State != runtimeowner.Stopping || current.OperationID != operationID || !reflect.DeepEqual(current.Owner, &verifiedOwner) {
			return completionErr
		}
		for _, command := range h.PostStop {
			if callCheckedHook(run, ctx, h.Binding.Target.Path, interpolateCommand(command, h.Port)) != hookSettledOK {
				_, err := store.MarkUncertain(ctx, h.Binding, &verifiedOwner, operationID, "post_stop_failed")
				completionErr = errors.Join(runtimeowner.ErrUncertain, err)
				return completionErr
			}
		}
		_, completionErr = store.Complete(ctx, h.Binding, verifiedOwner, operationID)
		if completionErr != nil {
			_, err := store.MarkUncertain(ctx, h.Binding, &verifiedOwner, operationID, "durability_failed")
			completionErr = errors.Join(completionErr, err)
		}
		return completionErr
	}
	return nil
}
