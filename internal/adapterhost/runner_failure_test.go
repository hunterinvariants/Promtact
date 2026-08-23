package adapterhost

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/dst"
	"github.com/hunterinvariants/promtact/internal/adapterproto"
)

func TestRunnerReturnsRemoteInvariantAsDSTViolation(t *testing.T) {
	host, adapter := net.Pipe()
	defer host.Close()

	done := serveScript(adapter, []scriptStep{
		{
			op: adapterproto.OpHello,
			respond: func(request adapterproto.Request) adapterproto.Response {
				return successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Hello = &adapterproto.HelloResponse{
							Nodes:      []uint32{1, 2},
							Invariants: []string{"at-most-one-token"},
						}
					},
				)
			},
		},
		{op: adapterproto.OpTick},
		{op: adapterproto.OpTick},
		{op: adapterproto.OpDrain},
		{op: adapterproto.OpDrain},
		{
			op: adapterproto.OpCheck,
			respond: func(request adapterproto.Request) adapterproto.Response {
				return successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Violation = &adapterproto.Violation{
							Invariant: "at-most-one-token",
							Detail:    "token held by nodes [1 2]",
						}
					},
				)
			},
		},
		{op: adapterproto.OpClose},
	})

	session, err := Open(host, host, 31)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	runner, err := NewRunner(dst.Config{Seed: 31}, session)
	if err != nil {
		t.Fatalf("NewRunner() failed: %v", err)
	}

	err = runner.StepChecked()
	if err == nil {
		t.Fatal("StepChecked() succeeded, want violation")
	}

	var violation *dst.Violation
	if !errors.As(err, &violation) {
		t.Fatalf(
			"StepChecked() error = %T(%v), want *dst.Violation",
			err,
			err,
		)
	}
	if violation.Invariant != "at-most-one-token" {
		t.Fatalf(
			"violation invariant = %q, want at-most-one-token",
			violation.Invariant,
		)
	}
	if violation.Step != 1 || violation.Step != runner.Now() {
		t.Fatalf(
			"violation step = %d, runner now = %d, want 1",
			violation.Step,
			runner.Now(),
		)
	}
	if violation.Trace != runner.TraceHash() {
		t.Fatalf(
			"violation trace = %s, runner trace = %s",
			violation.Trace,
			runner.TraceHash(),
		)
	}
	if violation.Err == nil ||
		violation.Err.Error() != "token held by nodes [1 2]" {
		t.Fatalf(
			"violation detail = %v, want conflicting token state",
			violation.Err,
		)
	}

	if err := runner.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("scripted adapter failed: %v", err)
	}
}

func TestRunnerAbortsCurrentStepOnCallbackFailure(t *testing.T) {
	host, adapter := net.Pipe()
	defer host.Close()

	done := serveScript(adapter, []scriptStep{
		{
			op: adapterproto.OpHello,
			respond: func(request adapterproto.Request) adapterproto.Response {
				return successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Hello = &adapterproto.HelloResponse{
							Nodes: []uint32{1},
						}
					},
				)
			},
		},
		{
			op: adapterproto.OpTick,
			respond: func(request adapterproto.Request) adapterproto.Response {
				return adapterproto.Response{
					Version: adapterproto.Version,
					ID:      request.ID,
					Op:      request.Op,
					Error: &adapterproto.RemoteError{
						Code:    "tick-failed",
						Message: "protocol node rejected tick",
					},
				}
			},
		},
		{op: adapterproto.OpClose},
	})

	session, err := Open(host, host, 32)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	runner, err := NewRunner(dst.Config{Seed: 32}, session)
	if err != nil {
		t.Fatalf("NewRunner() failed: %v", err)
	}

	err = runner.StepChecked()
	if err == nil {
		t.Fatal("StepChecked() succeeded, want callback failure")
	}

	var runError *RunError
	if !errors.As(err, &runError) {
		t.Fatalf(
			"StepChecked() error = %T(%v), want *RunError",
			err,
			err,
		)
	}
	if runError.Step != 1 || runError.Step != runner.Now() {
		t.Fatalf(
			"RunError step = %d, runner now = %d, want 1",
			runError.Step,
			runner.Now(),
		)
	}
	if runError.Trace != runner.TraceHash() {
		t.Fatalf(
			"RunError trace = %s, runner trace = %s",
			runError.Trace,
			runner.TraceHash(),
		)
	}
	if !strings.Contains(runError.Op, "tick node 1") {
		t.Fatalf(
			"RunError operation = %q, want tick node 1",
			runError.Op,
		)
	}

	var remote *RemoteError
	if !errors.As(runError, &remote) {
		t.Fatalf(
			"RunError cause = %T(%v), want *RemoteError",
			runError.Err,
			runError.Err,
		)
	}
	if remote.Code != "tick-failed" {
		t.Fatalf(
			"remote code = %q, want tick-failed",
			remote.Code,
		)
	}

	if err := runner.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("scripted adapter failed: %v", err)
	}
}

func TestNewRunnerRejectsMissingOrClosedSession(t *testing.T) {
	if _, err := NewRunner(dst.Config{}, nil); err == nil {
		t.Fatal("NewRunner(nil) succeeded, want error")
	}

	closed := &Session{closed: true}
	if _, err := NewRunner(dst.Config{}, closed); !errors.Is(err, ErrClosed) {
		t.Fatalf("NewRunner(closed) error = %v, want %v", err, ErrClosed)
	}
}
