package chaos

import (
	"context"
	"strings"
	"testing"
)

func TestApplyExplainsOwnershipRefusal(t *testing.T) {
	tests := []struct {
		name      string
		failAt    int
		claim     string
		forbidden string
	}{
		{
			name:      "pin root",
			failAt:    1,
			claim:     `pin root "/sys/fs/bpf/promtact-test"`,
			forbidden: "rm -f",
		},
		{
			name:      "namespace",
			failAt:    6,
			claim:     `namespace "promtact-test"`,
			forbidden: "ip netns del promtact-test",
		},
		{
			name:      "veth pair",
			failAt:    7,
			claim:     `veth pair "promtact-host" and "promtact-peer"`,
			forbidden: "ip link del promtact-host",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{failAt: test.failAt}
			controller, err := New(Plan{
				Namespace: "promtact-test",
				HostVeth:  "promtact-host",
				PeerVeth:  "promtact-peer",
				BPFObject: "chaos.o",
			}, runner)
			if err != nil {
				t.Fatal(err)
			}

			applyErr := controller.Apply(context.Background())
			if applyErr == nil {
				t.Fatal("expected ownership refusal")
			}
			for _, want := range []string{
				"chaos: cannot claim " + test.claim,
				"refusing to use or remove it without ownership",
				"injected command failure",
			} {
				if !strings.Contains(applyErr.Error(), want) {
					t.Fatalf("error %q does not contain %q", applyErr, want)
				}
			}

			joined := strings.Join(runner.calls, "\n")
			if strings.Contains(joined, test.forbidden) {
				t.Fatalf("unowned resource cleanup attempted:\n%s", joined)
			}
		})
	}
}
