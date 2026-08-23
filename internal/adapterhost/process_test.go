package adapterhost

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/internal/adapterproto"
)

func processTestOptions(
	mode string,
	stderr *bytes.Buffer,
) ProcessOptions {
	options := ProcessOptions{
		Command: os.Args[0],
		Args: []string{
			"-test.run=^TestAdapterProcessHelper$",
		},
		Env: append(
			os.Environ(),
			processHelperModeEnv+"="+mode,
		),
	}
	if stderr != nil {
		options.Stderr = stderr
	}
	return options
}

func TestStartProcessRejectsInvalidOptions(t *testing.T) {
	if _, err := StartProcess(
		nil,
		ProcessOptions{Command: os.Args[0]},
		1,
	); err == nil ||
		!strings.Contains(err.Error(), "context is required") {
		t.Fatalf(
			"StartProcess(nil context) error = %v, want context error",
			err,
		)
	}

	if _, err := StartProcess(
		context.Background(),
		ProcessOptions{},
		1,
	); err == nil ||
		!strings.Contains(err.Error(), "command is required") {
		t.Fatalf(
			"StartProcess(empty command) error = %v, want command error",
			err,
		)
	}
}

func TestProcessLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	var mirroredStderr bytes.Buffer

	process, err := StartProcess(
		ctx,
		processTestOptions("success", &mirroredStderr),
		73,
	)
	if err != nil {
		t.Fatalf("StartProcess() failed: %v", err)
	}

	session := process.Session()
	if session == nil {
		t.Fatal("Session() = nil")
	}

	if got, want := session.Nodes(), []uint32{1, 2}; !slices.Equal(got, want) {
		t.Fatalf("Nodes() = %v, want %v", got, want)
	}
	if got, want := session.Invariants(), []string{
		"at-most-one-token",
	}; !slices.Equal(got, want) {
		t.Fatalf("Invariants() = %v, want %v", got, want)
	}

	if err := session.Tick(1); err != nil {
		t.Fatalf("Tick() failed: %v", err)
	}

	messages, err := session.Drain(1)
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("Drain() returned %d messages, want 1", len(messages))
	}

	wantMessage := adapterproto.Message{
		From:    1,
		To:      2,
		Kind:    7,
		Value:   99,
		Payload: []byte("token"),
	}
	gotMessage := messages[0]
	if gotMessage.From != wantMessage.From ||
		gotMessage.To != wantMessage.To ||
		gotMessage.Kind != wantMessage.Kind ||
		gotMessage.Value != wantMessage.Value ||
		!bytes.Equal(gotMessage.Payload, wantMessage.Payload) {
		t.Fatalf("Drain() message = %#v, want %#v", gotMessage, wantMessage)
	}

	if err := session.Deliver(
		gotMessage.To,
		gotMessage,
	); err != nil {
		t.Fatalf("Deliver() failed: %v", err)
	}

	violation, err := session.Check()
	if err != nil {
		t.Fatalf("Check() failed: %v", err)
	}
	if violation != nil {
		t.Fatalf("Check() violation = %#v, want nil", violation)
	}

	if got := process.Stderr(); !strings.Contains(
		got,
		"token-ring adapter ready",
	) {
		t.Fatalf("Stderr() = %q, want startup diagnostic", got)
	}

	if err := process.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("second Close() failed: %v", err)
	}

	if got := mirroredStderr.String(); !strings.Contains(
		got,
		"token-ring adapter ready",
	) {
		t.Fatalf(
			"mirrored stderr = %q, want startup diagnostic",
			got,
		)
	}
}
