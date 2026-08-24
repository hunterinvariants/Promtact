package chaos

import (
	"context"
	"strings"
	"testing"
)

func TestApplyUpdatesSharedPolicyBeforeAttach(t *testing.T) {
	runner := &fakeRunner{}
	plan := Plan{
		Namespace: "promtact-test",
		HostVeth:  "promtact-host",
		PeerVeth:  "promtact-peer",
		BPFObject: "chaos.o",
		Policy: Policy{
			XDPDropPct:   10,
			TCDropPct:    2.5,
			TCCorruptPct: 1,
		},
	}
	controller, err := New(plan, runner)
	if err != nil {
		t.Fatal(err)
	}

	if err := controller.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(runner.calls, "\n")
	if got := strings.Count(joined, "chaos.o"); got != 1 {
		t.Fatalf("BPF object loaded %d times, want once:\n%s", got, joined)
	}

	pins := pinsFor(plan.Namespace)
	value, err := plan.Policy.mapValue()
	if err != nil {
		t.Fatal(err)
	}

	create := "bpftool map create " + pins.policy +
		" type array key 4 value 20 entries 1 name policy"
	load := "bpftool prog loadall chaos.o " + pins.programs +
		" map name policy pinned " + pins.policy
	update := strings.Join(policyUpdateStep(pins.policy, value), " ")
	netns := "nsenter --net=/var/run/netns/promtact-test -- "
	xdp := netns + "ip link set promtact-peer xdp pinned " + pins.xdp
	tc := netns + "tc filter add dev promtact-peer egress bpf object-pinned " +
		pins.tc + " direct-action"

	createAt := strings.Index(joined, create)
	loadAt := strings.Index(joined, load)
	updateAt := strings.Index(joined, update)
	xdpAt := strings.Index(joined, xdp)
	tcAt := strings.Index(joined, tc)

	if createAt < 0 || updateAt < 0 || loadAt < 0 || xdpAt < 0 || tcAt < 0 {
		t.Fatalf("required command missing:\n%s", joined)
	}
	if !(createAt < updateAt && updateAt < loadAt && loadAt < xdpAt && xdpAt < tcAt) {
		t.Fatalf("unsafe command order:\n%s", joined)
	}
}

func TestCloseRemovesOnlyNamespaceScopedPins(t *testing.T) {
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

	joined := strings.Join(runner.calls, "\n")
	pins := pinsFor("promtact-test")
	wantRemove := "rm -f -- " + pins.xdp + " " + pins.tc + " " + pins.policy
	wantDirectories := "rmdir -- " +
		pins.programs + " " + pins.maps + " " + pins.root

	if !strings.Contains(joined, wantRemove) {
		t.Fatalf("specific pin removal missing:\n%s", joined)
	}
	if !strings.Contains(joined, wantDirectories) {
		t.Fatalf("pin directory cleanup missing:\n%s", joined)
	}
	if strings.Contains(joined, "rm -rf") {
		t.Fatalf("recursive removal used:\n%s", joined)
	}
}

func TestRejectsInvalidBPFPolicy(t *testing.T) {
	_, err := New(Plan{
		Namespace: "promtact-test",
		HostVeth:  "promtact-host",
		PeerVeth:  "promtact-peer",
		BPFObject: "chaos.o",
		Policy:    Policy{XDPDropPct: 101},
	}, &fakeRunner{})
	if err == nil || !strings.Contains(err.Error(), "XDP drop percentage") {
		t.Fatalf("New() = %v, want invalid XDP policy", err)
	}
}
