package chaos

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls  []string
	failAt int
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.failAt > 0 && len(f.calls) == f.failAt {
		return errors.New("injected command failure")
	}
	return nil
}

func TestRejectsHostInterfaceNames(t *testing.T) {
	_, err := New(Plan{
		Namespace: "promtact-test", HostVeth: "eth0",
		PeerVeth: "promtact-peer", BPFObject: "chaos.o",
	}, &fakeRunner{})
	if err == nil {
		t.Fatal("accepted non-Promtact host interface")
	}
}

func TestFailureAlwaysRunsNamespaceCleanup(t *testing.T) {
	runner := &fakeRunner{failAt: 8}
	controller, err := New(Plan{
		Namespace: "promtact-test", HostVeth: "promtact-host",
		PeerVeth: "promtact-peer", BPFObject: "chaos.o",
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Apply(context.Background()); err == nil {
		t.Fatal("expected injected failure")
	}
	joined := strings.Join(runner.calls, "\n")
	if !strings.Contains(joined, "ip netns del promtact-test") ||
		!strings.Contains(joined, "ip link del promtact-host") {
		t.Fatalf("cleanup missing:\n%s", joined)
	}
}
