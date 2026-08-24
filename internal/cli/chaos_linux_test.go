//go:build linux

package cli

import (
	"io"
	"net/netip"
	"testing"
	"time"
)

func TestChaosOptionsKeepNetemAndBPFPoliciesSeparate(t *testing.T) {
	flags, options := newChaosFlagSet(io.Discard)
	err := flags.Parse([]string{
		"-bpf-object", "chaos.o",
		"-delay", "25ms",
		"-loss", "10",
		"-xdp-drop", "20",
		"-tc-drop", "30",
		"-tc-corrupt", "5",
		"-block-source", "192.0.2.10",
		"-block-destination", "198.51.100.20",
		"-yes-really",
	})
	if err != nil {
		t.Fatal(err)
	}

	plan, err := options.plan()
	if err != nil {
		t.Fatal(err)
	}

	if plan.Delay != 25*time.Millisecond || plan.LossPct != 10 {
		t.Fatalf("netem = (%s, %v), want (25ms, 10)", plan.Delay, plan.LossPct)
	}
	if plan.Policy.XDPDropPct != 20 ||
		plan.Policy.TCDropPct != 30 ||
		plan.Policy.TCCorruptPct != 5 {
		t.Fatalf("BPF policy = %#v", plan.Policy)
	}
	if plan.Policy.BlockedSrc != netip.MustParseAddr("192.0.2.10") {
		t.Fatalf("blocked source = %v", plan.Policy.BlockedSrc)
	}
	if plan.Policy.BlockedDst != netip.MustParseAddr("198.51.100.20") {
		t.Fatalf("blocked destination = %v", plan.Policy.BlockedDst)
	}
	if !options.confirm {
		t.Fatal("safety acknowledgement not parsed")
	}
}

func TestChaosFlagsExposeExplicitPolicyControls(t *testing.T) {
	flags, _ := newChaosFlagSet(io.Discard)

	for _, name := range []string{
		"loss",
		"xdp-drop",
		"tc-drop",
		"tc-corrupt",
		"block-source",
		"block-destination",
	} {
		if flags.Lookup(name) == nil {
			t.Errorf("flag %q is missing", name)
		}
	}

	if got := flags.Lookup("loss").Usage; got != "netem loss percentage" {
		t.Fatalf("-loss description = %q", got)
	}
}

func TestChaosOptionsRejectInvalidBlockedAddress(t *testing.T) {
	flags, options := newChaosFlagSet(io.Discard)
	if err := flags.Parse([]string{
		"-bpf-object", "chaos.o",
		"-block-source", "2001:db8::1",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := options.plan(); err == nil {
		t.Fatal("accepted an IPv6 blocked source")
	}
}
