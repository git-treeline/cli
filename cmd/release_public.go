package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"

	"github.com/git-treeline/cli/internal/registry"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const maxReleaseRequestBytes = 64 * 1024

type releaseExecutorFactory func(*registry.Registry) (*releaseExecutor, error)

func newReleaseStartCommand(registryPath string, factory releaseExecutorFactory) *cobra.Command {
	return newPublicReleaseCommand(registryPath, factory, false)
}

func newReleaseResumeCommand(registryPath string, factory releaseExecutorFactory) *cobra.Command {
	return newPublicReleaseCommand(registryPath, factory, true)
}

func newPublicReleaseCommand(registryPath string, factory releaseExecutorFactory, resume bool) *cobra.Command {
	use, short := "start", "Execute the exact release request supplied as JSON on stdin"
	if resume {
		use, short = "resume <operation-id>", "Resume the retained release intent without replacing its bindings"
	}
	command := &cobra.Command{
		Use: use, Short: short, SilenceErrors: true, SilenceUsage: true,
		PersistentPreRunE:  guardAppManagedDev,
		PersistentPostRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			response := releaseStatus{Schema: 1}
			var intent registry.ReleaseRequest
			if resume {
				if len(args) != 1 || !publicReleaseUUID(args[0]) {
					return writePublicReleaseStatus(cmd, response, "invalid_operation_id")
				}
				response.OperationID = args[0]
			} else {
				if len(args) != 0 {
					return writePublicReleaseStatus(cmd, response, "invalid_request")
				}
				var err error
				intent, err = decodeReleaseRequest(cmd.InOrStdin())
				if err != nil || !publicReleaseUUID(intent.OperationID) {
					return writePublicReleaseStatus(cmd, response, "invalid_request")
				}
				response.OperationID = intent.OperationID
			}
			reg := registry.New(registryPath)
			retained, err := reg.GetRelease(response.OperationID)
			if err != nil {
				return writePublicReleaseStatus(cmd, response, "registry_unavailable")
			}
			response.Operation = retained
			if resume {
				if retained == nil {
					return writePublicReleaseStatus(cmd, response, "not_found")
				}
				intent = cloneReleaseOperation(*retained).ReleaseRequest
			} else if retained != nil && !reflect.DeepEqual(retained.ReleaseRequest, intent) {
				return writePublicReleaseStatus(cmd, response, "binding_conflict")
			}
			if retained != nil && retained.Outcome() == "completed" {
				return writePublicReleaseStatus(cmd, response, "")
			}
			if factory == nil {
				return writePublicReleaseStatus(cmd, response, "executor_unconfigured")
			}
			executor, err := factory(reg)
			if err != nil {
				return writePublicReleaseStatus(cmd, response, "executor_unavailable")
			}
			if executor == nil || executor.Registry == nil || executor.Registry.Path != reg.Path {
				return writePublicReleaseStatus(cmd, response, "executor_unconfigured")
			}
			if resume {
				_, err = executor.Resume(cmd.Context(), intent)
			} else {
				_, err = executor.Start(cmd.Context(), intent)
			}
			code := publicReleaseErrorCode(err)
			latest, readErr := reg.GetRelease(response.OperationID)
			if readErr != nil {
				response.Operation = nil
				return writePublicReleaseStatus(cmd, response, "registry_unavailable")
			}
			response.Operation = latest
			if code == "" && latest == nil {
				code = "receipt_unavailable"
			}
			return writePublicReleaseStatus(cmd, response, code)
		},
	}
	command.SetFlagErrorFunc(func(cmd *cobra.Command, _ error) error {
		return writePublicReleaseStatus(cmd, releaseStatus{Schema: 1}, "invalid_request")
	})
	return command
}

func publicReleaseUUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && len(id) == 36 && parsed.String() == id
}

func writePublicReleaseStatus(cmd *cobra.Command, response releaseStatus, code string) error {
	response.Outcome = code
	if code == "" && response.Operation != nil {
		response.Outcome = response.Operation.Outcome()
	}
	if err := json.NewEncoder(cmd.OutOrStdout()).Encode(response); err != nil {
		return errors.New("output_unavailable")
	}
	if code != "" {
		return errors.New(code)
	}
	return nil
}

func publicReleaseErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var refusal *releaseExecutionError
	if errors.As(err, &refusal) {
		switch refusal.Code {
		case "executor_unconfigured", "execution_lock_unavailable", "registry_unavailable", "binding_conflict", "not_found", "reservation_refused", "prepare_refused", "uncertain", "cancelled", "preflight_blocked", "preflight_unavailable", "phase_refused", "receipt_unavailable", "blocked":
			return refusal.Code
		}
	}
	return "execution_failed"
}

func decodeReleaseRequest(reader io.Reader) (registry.ReleaseRequest, error) {
	invalid := errors.New("invalid_request")
	raw, err := io.ReadAll(io.LimitReader(reader, maxReleaseRequestBytes+1))
	if err != nil || len(raw) > maxReleaseRequestBytes {
		return registry.ReleaseRequest{}, invalid
	}
	if err := uniqueReleaseRequestKeys(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return registry.ReleaseRequest{}, invalid
	}
	var request registry.ReleaseRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return registry.ReleaseRequest{}, invalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return registry.ReleaseRequest{}, invalid
	}
	canonical, err := json.Marshal(request)
	if err != nil {
		return registry.ReleaseRequest{}, invalid
	}
	decode := func(data []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	original, err := decode(raw)
	if err != nil {
		return registry.ReleaseRequest{}, invalid
	}
	normalized, err := decode(canonical)
	if err != nil || !reflect.DeepEqual(original, normalized) {
		return registry.ReleaseRequest{}, invalid
	}
	return request, nil
}

func uniqueReleaseRequestKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		seen := map[string]bool{}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || seen[key] {
				return errors.New("duplicate_request_key")
			}
			seen[key] = true
			if err := uniqueReleaseRequestKeys(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case json.Delim('['):
		for decoder.More() {
			if err := uniqueReleaseRequestKeys(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	return nil
}
