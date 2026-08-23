package adapterhost

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStartProcessReportsHandshakeError(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	process, err := StartProcess(
		ctx,
		processTestOptions("handshake-error", nil),
		81,
	)
	if err == nil {
		if process != nil {
			_ = process.Close()
		}
		t.Fatal("StartProcess() succeeded, want handshake error")
	}
	if process != nil {
		t.Fatalf("StartProcess() process = %#v, want nil", process)
	}

	var remote *RemoteError
	if !errors.As(err, &remote) {
		t.Fatalf(
			"StartProcess() error = %T(%v), want *RemoteError",
			err,
			err,
		)
	}
	if remote.Code != "handshake-rejected" ||
		remote.Message != "adapter cannot initialize" {
		t.Fatalf(
			"remote error = %#v, want handshake rejection",
			remote,
		)
	}

	if !strings.Contains(
		err.Error(),
		"adapter configuration rejected",
	) {
		t.Fatalf(
			"StartProcess() error = %q, want captured stderr",
			err,
		)
	}
}

func TestStartProcessBoundsHandshakeStderr(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	process, err := StartProcess(
		ctx,
		processTestOptions("stderr-overflow", nil),
		82,
	)
	if err == nil {
		if process != nil {
			_ = process.Close()
		}
		t.Fatal("StartProcess() succeeded, want handshake error")
	}
	if process != nil {
		t.Fatalf("StartProcess() process = %#v, want nil", process)
	}

	if !strings.Contains(err.Error(), "[stderr truncated]") {
		t.Fatalf(
			"StartProcess() error lacks truncation marker: %q",
			err,
		)
	}
	if len(err.Error()) > MaxCapturedStderr+1024 {
		t.Fatalf(
			"StartProcess() error length = %d, want at most %d",
			len(err.Error()),
			MaxCapturedStderr+1024,
		)
	}
}

func TestStartProcessHonorsContextDuringHandshake(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		300*time.Millisecond,
	)
	defer cancel()

	started := time.Now()

	process, err := StartProcess(
		ctx,
		processTestOptions("blocked-handshake", nil),
		83,
	)
	elapsed := time.Since(started)

	if err == nil {
		if process != nil {
			_ = process.Close()
		}
		t.Fatal("StartProcess() succeeded, want context termination")
	}
	if process != nil {
		t.Fatalf("StartProcess() process = %#v, want nil", process)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf(
			"context error = %v, want %v",
			ctx.Err(),
			context.DeadlineExceeded,
		)
	}
	if elapsed > 3*time.Second {
		t.Fatalf(
			"StartProcess() returned after %v, want below 3s",
			elapsed,
		)
	}
}

func TestProcessConcurrentClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	process, err := StartProcess(
		ctx,
		processTestOptions("success", nil),
		84,
	)
	if err != nil {
		t.Fatalf("StartProcess() failed: %v", err)
	}

	const callers = 8

	errorsByCaller := make(chan error, callers)
	var group sync.WaitGroup
	group.Add(callers)

	for range callers {
		go func() {
			defer group.Done()
			errorsByCaller <- process.Close()
		}()
	}

	group.Wait()
	close(errorsByCaller)

	for err := range errorsByCaller {
		if err != nil {
			t.Fatalf("concurrent Close() failed: %v", err)
		}
	}
}
