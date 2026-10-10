package supervisor

import (
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testRuntimeGeneration = "11111111-1111-4111-8111-111111111111"
const testOtherGeneration = "22222222-2222-4222-8222-222222222222"
const testShutdownOperation = "33333333-3333-4333-8333-333333333333"
const testOtherOperation = "44444444-4444-4444-8444-444444444444"

func generationRequest(generation, operation string) string {
	return generationShutdownCommand + ":" + generation + ":" + operation
}

func generationResponse(generation, operation string) string {
	return generationShutdownAcknowledgement + ":" + generation + ":" + operation
}

func TestGenerationShutdownRejectsUnboundStaleAndMalformed(t *testing.T) {
	valid := generationRequest(testRuntimeGeneration, testShutdownOperation)
	for _, request := range []string{
		settledShutdownCommand, generationShutdownCommand, generationShutdownCommand + ":",
		generationRequest(testOtherGeneration, testShutdownOperation),
		generationRequest("00000000-0000-0000-0000-000000000000", testShutdownOperation),
		generationRequest(testRuntimeGeneration, ""), generationRequest(testRuntimeGeneration, "invalid"),
		valid + ":extra", valid + "\n", " " + valid,
	} {
		t.Run(request, func(t *testing.T) {
			s := settledSupervisor()
			s.RuntimeGeneration = testRuntimeGeneration
			s.ShutdownRequested = func(string) error { t.Fatal("invalid request registered shutdown"); return nil }
			s.ShutdownComplete = func() error { t.Fatal("invalid request completed shutdown"); return nil }
			conn, done := settledRequest(s, request)
			awaitSettledTest(t, done)
			if !strings.HasPrefix(conn.response(), "error:") || s.terminal {
				t.Fatalf("invalid request affected lifecycle: %q", conn.response())
			}
		})
	}
}

func TestGenerationShutdownUntrackedRefusesBoundRequest(t *testing.T) {
	s := settledSupervisor()
	s.ShutdownComplete = func() error { t.Fatal("untracked owner shut down"); return nil }
	conn, done := settledRequest(s, generationRequest(testRuntimeGeneration, testShutdownOperation))
	awaitSettledTest(t, done)
	if !strings.HasPrefix(conn.response(), "error:") || s.terminal {
		t.Fatal("untracked owner accepted generation")
	}
}

func TestGenerationShutdownConfigurationIsValidatedAndFrozen(t *testing.T) {
	for _, value := range []string{"INVALID", "00000000-0000-0000-0000-000000000000", "{11111111-1111-4111-8111-111111111111}", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"} {
		s := settledSupervisor()
		s.RuntimeGeneration = value
		s.ShutdownComplete = func() error { return nil }
		if s.prepareRuntimeGeneration() == nil {
			t.Fatalf("accepted invalid UUID %q", value)
		}
	}
	s := settledSupervisor()
	s.RuntimeGeneration = testRuntimeGeneration
	if s.prepareRuntimeGeneration() == nil {
		t.Fatal("tracked owner without completion callback accepted")
	}
	s = settledSupervisor()
	s.RuntimeGeneration = testRuntimeGeneration
	s.ShutdownComplete = func() error { return nil }
	if err := s.prepareRuntimeGeneration(); err != nil {
		t.Fatal(err)
	}
	s.RuntimeGeneration = testOtherGeneration
	conn, done := settledRequest(s, generationRequest(testOtherGeneration, testShutdownOperation))
	awaitSettledTest(t, done)
	if !strings.HasPrefix(conn.response(), "error:") || s.terminal {
		t.Fatal("runtime generation mutated")
	}
	conn, done = settledRequest(s, generationRequest(testRuntimeGeneration, testShutdownOperation))
	awaitSettledTest(t, done)
	if conn.response() != generationResponse(testRuntimeGeneration, testShutdownOperation) {
		t.Fatal("frozen generation rejected")
	}
}

func TestGenerationShutdownWaitsAndBindsRetryToOperation(t *testing.T) {
	s := settledSupervisor()
	s.RuntimeGeneration = testRuntimeGeneration
	entered, release := make(chan struct{}), make(chan struct{})
	var requests, completions atomic.Int32
	s.ShutdownRequested = func(operation string) error {
		if operation != testShutdownOperation {
			t.Error("wrong operation registered")
		}
		requests.Add(1)
		return nil
	}
	s.ShutdownComplete = func() error { completions.Add(1); close(entered); <-release; return nil }
	first, firstDone := settledRequest(s, generationRequest(testRuntimeGeneration, testShutdownOperation))
	awaitSettledTest(t, entered)
	status, statusDone := settledRequest(s, generationStatusCommand+":"+testRuntimeGeneration)
	awaitSettledTest(t, statusDone)
	if state, err := generationStatusResult(status.response(), testRuntimeGeneration, nil); err != nil || state != "stopping" {
		t.Fatalf("status %q: %v", state, err)
	}
	select {
	case <-firstDone:
		t.Fatal("acknowledged before completion")
	default:
	}
	retry, retryDone := settledRequest(s, generationRequest(testRuntimeGeneration, testShutdownOperation))
	other, otherDone := settledRequest(s, generationRequest(testRuntimeGeneration, testOtherOperation))
	close(release)
	awaitSettledTest(t, firstDone)
	awaitSettledTest(t, retryDone)
	awaitSettledTest(t, otherDone)
	if first.response() != generationResponse(testRuntimeGeneration, testShutdownOperation) || retry.response() != first.response() {
		t.Fatal("same operation lost success")
	}
	if !strings.HasPrefix(other.response(), "error:") || requests.Load() != 1 || completions.Load() != 1 {
		t.Fatal("different operation replayed completion")
	}
	status, statusDone = settledRequest(s, generationStatusCommand+":"+testRuntimeGeneration)
	awaitSettledTest(t, statusDone)
	if state, err := generationStatusResult(status.response(), testRuntimeGeneration, nil); err != nil || state != "settled" {
		t.Fatalf("status %q: %v", state, err)
	}
}

func TestGenerationShutdownRequestedPrecedesStopAndFailurePreventsEffects(t *testing.T) {
	for _, failure := range []string{"none", "error", "panic"} {
		t.Run(failure, func(t *testing.T) {
			s := settledSupervisor()
			s.RuntimeGeneration = testRuntimeGeneration
			requested, stopped, completed := false, false, false
			s.ShutdownRequested = func(operation string) error {
				requested = true
				if operation != testShutdownOperation || stopped || completed {
					t.Fatal("registration order or operation incorrect")
				}
				if failure == "error" {
					return errors.New("durable registration failed")
				}
				if failure == "panic" {
					panic("registration failed")
				}
				return nil
			}
			s.ShutdownComplete = func() error {
				completed = true
				if !stopped {
					t.Fatal("completion preceded stop")
				}
				return nil
			}
			err := s.shutdownOperationWithStop(testShutdownOperation, func() error {
				stopped = true
				if !requested {
					t.Fatal("stop preceded registration")
				}
				return nil
			})
			if failure == "none" {
				if err != nil || !completed {
					t.Fatal("success did not settle")
				}
				return
			}
			if err == nil || stopped || completed {
				t.Fatal("failed registration caused effects")
			}
			select {
			case <-s.done:
				t.Fatal("failed registration released owner")
			default:
			}
			if s.shutdownOperationWithStop(testShutdownOperation, func() error { t.Fatal("failure replayed stop"); return nil }) != err {
				t.Fatal("failure changed")
			}
			status, done := settledRequest(s, generationStatusCommand+":"+testRuntimeGeneration)
			awaitSettledTest(t, done)
			if state, e := generationStatusResult(status.response(), testRuntimeGeneration, nil); e != nil || state != "failed" {
				t.Fatalf("status %q: %v", state, e)
			}
		})
	}
}

func TestGenerationShutdownLostReplyRetainsExactOutcome(t *testing.T) {
	s := settledSupervisor()
	s.RuntimeGeneration = testRuntimeGeneration
	calls := 0
	s.ShutdownComplete = func() error { calls++; return nil }
	conn := &settledTestConn{input: strings.NewReader(generationRequest(testRuntimeGeneration, testShutdownOperation)), writeErr: io.ErrClosedPipe}
	s.handleConn(conn)
	if generationShutdownResult(conn.response(), testRuntimeGeneration, testShutdownOperation, io.ErrClosedPipe) == nil {
		t.Fatal("lost reply accepted")
	}
	retry, done := settledRequest(s, generationRequest(testRuntimeGeneration, testShutdownOperation))
	awaitSettledTest(t, done)
	if generationShutdownResult(retry.response(), testRuntimeGeneration, testShutdownOperation, nil) != nil || calls != 1 {
		t.Fatal("lost reply replayed cleanup")
	}
}

func TestGenerationShutdownClientStrictResponse(t *testing.T) {
	for _, response := range []string{"", "ok", settledShutdownAcknowledgement,
		generationResponse(testOtherGeneration, testShutdownOperation), generationResponse(testRuntimeGeneration, testOtherOperation),
		generationResponse(testRuntimeGeneration, testShutdownOperation) + "\n", "error: shutdown not settled",
		"unsupported: " + generationShutdownCommand, "unknown command: " + generationShutdownCommand,
	} {
		if generationShutdownResult(response, testRuntimeGeneration, testShutdownOperation, nil) == nil {
			t.Fatalf("accepted %q", response)
		}
	}
	valid := generationResponse(testRuntimeGeneration, testShutdownOperation)
	if generationShutdownResult(valid, testRuntimeGeneration, testShutdownOperation, nil) != nil {
		t.Fatal("exact acknowledgement refused")
	}
	if generationShutdownResult(valid, testRuntimeGeneration, testShutdownOperation, io.EOF) == nil {
		t.Fatal("partial transport failure accepted")
	}
	if ShutdownSettledGeneration("unused.sock", "invalid", testShutdownOperation, time.Second) == nil {
		t.Fatal("invalid generation accepted")
	}
	if ShutdownSettledGeneration("unused.sock", testRuntimeGeneration, "invalid", time.Second) == nil {
		t.Fatal("invalid operation accepted")
	}
}

func TestGenerationStatusRejectsMismatchesWithoutEffects(t *testing.T) {
	s := settledSupervisor()
	s.RuntimeGeneration = testRuntimeGeneration
	s.ShutdownComplete = func() error { t.Fatal("status caused shutdown"); return nil }
	for _, generation := range []string{testRuntimeGeneration, testOtherGeneration, "invalid"} {
		conn, done := settledRequest(s, generationStatusCommand+":"+generation)
		awaitSettledTest(t, done)
		state, err := generationStatusResult(conn.response(), generation, nil)
		if generation == testRuntimeGeneration {
			if err != nil || state != "owned" {
				t.Fatal("owner status missing")
			}
		} else if err == nil {
			t.Fatal("mismatched status accepted")
		}
		if s.terminal {
			t.Fatal("status affected lifecycle")
		}
	}
	response := generationStatusCommand + ":" + testRuntimeGeneration + ":settled"
	if _, err := generationStatusResult(response, testOtherGeneration, nil); err == nil {
		t.Fatal("wrong owner accepted")
	}
	if _, err := generationStatusResult(response+"\n", testRuntimeGeneration, nil); err == nil {
		t.Fatal("malformed response accepted")
	}
	if _, err := generationStatusResult(response, testRuntimeGeneration, io.EOF); err == nil {
		t.Fatal("transport failure accepted")
	}
}

type generationReadErrorConn struct{ *settledTestConn }

func (c generationReadErrorConn) Read(p []byte) (int, error) {
	n, err := c.settledTestConn.Read(p)
	if err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func TestGenerationShutdownRejectsIncompleteTransport(t *testing.T) {
	s := settledSupervisor()
	s.RuntimeGeneration = testRuntimeGeneration
	s.ShutdownComplete = func() error { t.Fatal("incomplete request stopped owner"); return nil }
	conn := generationReadErrorConn{&settledTestConn{input: strings.NewReader(generationRequest(testRuntimeGeneration, testShutdownOperation))}}
	s.handleConn(conn)
	if !strings.HasPrefix(conn.response(), "error:") || s.terminal {
		t.Fatal("request accepted despite transport failure")
	}
}

func TestGenerationShutdownOrdinaryRouteRetainsCompletion(t *testing.T) {
	s := settledSupervisor()
	s.RuntimeGeneration = testRuntimeGeneration
	requested, completed := false, false
	s.ShutdownRequested = func(operation string) error {
		if operation != "" {
			t.Fatal("ordinary shutdown fabricated operation")
		}
		requested = true
		return nil
	}
	s.ShutdownComplete = func() error { completed = true; return nil }
	conn, done := settledRequest(s, "shutdown")
	awaitSettledTest(t, done)
	if conn.response() != "ok" || !requested || !completed {
		t.Fatal("ordinary route bypassed tracked completion")
	}
	conn, done = settledRequest(s, generationRequest(testRuntimeGeneration, testShutdownOperation))
	awaitSettledTest(t, done)
	if !strings.HasPrefix(conn.response(), "error:") {
		t.Fatal("unbound completion attested a later operation")
	}
}

func TestGenerationStartupNotifiesBeforeServingAndOnlyAfterLaunch(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := settledSupervisor()
		var order []string
		s.Started = func() { order = append(order, "started") }
		launchErr := errors.New("fixture launch failed")
		err := s.startAndServe(func() error {
			order = append(order, "launch")
			if fail {
				return launchErr
			}
			return nil
		}, func() { order = append(order, "serve") })
		if fail {
			if err != launchErr || strings.Join(order, ",") != "launch" {
				t.Fatal("failed start notified or served")
			}
		} else if err != nil || strings.Join(order, ",") != "launch,started,serve" {
			t.Fatal("runtime served before initial child launch notification")
		}
	}
}
