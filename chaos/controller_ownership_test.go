package chaos

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestFailureBeforeOwnershipDoesNotDeleteExistingResources(t *testing.T) {
	runner := &fakeRunner{failAt: 1}
	controller, err := New(Plan{
		Namespace: "promtact-test",
		HostVeth:  "promtact-host",
		PeerVeth:  "promtact-peer",
		BPFObject: "chaos.o",
	}, runner)
	if err != nil {
		t.Fatal(err)
	}

	if err := controller.Apply(context.Background()); err == nil {
		t.Fatal("expected pin-root collision")
	}

	joined := strings.Join(runner.calls, "\n")
	if strings.Contains(joined, "ip netns del") ||
		strings.Contains(joined, "ip link del") ||
		strings.Contains(joined, "rm -f") {
		t.Fatalf("unowned resource cleanup attempted:\n%s", joined)
	}
}

func TestNamespaceCollisionDoesNotDeleteExistingNamespace(t *testing.T) {
	runner := &fakeRunner{failAt: 6}
	controller, err := New(Plan{
		Namespace: "promtact-test",
		HostVeth:  "promtact-host",
		PeerVeth:  "promtact-peer",
		BPFObject: "chaos.o",
	}, runner)
	if err != nil {
		t.Fatal(err)
	}

	if err := controller.Apply(context.Background()); err == nil {
		t.Fatal("expected namespace collision")
	}

	joined := strings.Join(runner.calls, "\n")
	if strings.Contains(joined, "ip netns del promtact-test") {
		t.Fatalf("unowned namespace deletion attempted:\n%s", joined)
	}
	if !strings.Contains(joined, "rm -f -- /sys/fs/bpf/promtact-test/") {
		t.Fatalf("owned pins were not cleaned:\n%s", joined)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	runner := &fakeRunner{}
	controller, err := New(Plan{
		Namespace: "promtact-test",
		HostVeth:  "promtact-host",
		PeerVeth:  "promtact-peer",
		BPFObject: "chaos.o",
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	callCount := len(runner.calls)

	if err := controller.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != callCount {
		t.Fatalf("second Close added calls:\n%s", strings.Join(runner.calls, "\n"))
	}
}

func TestPlanRejectsUnsafeAdditionalValues(t *testing.T) {
	tests := []Plan{
		{
			Namespace: "promtact-test",
			HostVeth:  "promtact-same",
			PeerVeth:  "promtact-same",
			BPFObject: "chaos.o",
		},
		{
			Namespace: "promtact-test",
			HostVeth:  "promtact-host",
			PeerVeth:  "promtact-peer",
			BPFObject: "chaos.o",
			LossPct:   math.NaN(),
		},
	}

	for _, plan := range tests {
		if _, err := New(plan, &fakeRunner{}); err == nil {
			t.Fatalf("accepted unsafe plan: %#v", plan)
		}
	}
}

func TestCloseReportsPinCleanupFailureAndRetries(t *testing.T) {
	runner := &fakeRunner{failAt: 20}
	controller, err := New(Plan{
		Namespace: "promtact-test",
		HostVeth:  "promtact-host",
		PeerVeth:  "promtact-peer",
		BPFObject: "chaos.o",
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}

	err = controller.Close(context.Background())
	if err == nil || !strings.Contains(err.Error(), "injected command failure") {
		t.Fatalf("first Close() = %v, want cleanup failure", err)
	}

	firstCloseCalls := len(runner.calls)
	if err := controller.Close(context.Background()); err != nil {
		t.Fatalf("second Close() = %v", err)
	}

	retry := runner.calls[firstCloseCalls:]
	if len(retry) != 2 {
		t.Fatalf("retry calls = %v, want pin removal and directory cleanup", retry)
	}
	if !strings.HasPrefix(retry[0], "rm -f -- ") {
		t.Fatalf("retry did not remove pins: %v", retry)
	}
	if !strings.HasPrefix(retry[1], "rmdir -- ") {
		t.Fatalf("retry did not remove pin directories: %v", retry)
	}
}
