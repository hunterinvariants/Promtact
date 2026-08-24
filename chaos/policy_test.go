package chaos

import (
	"bytes"
	"encoding/binary"
	"math"
	"net/netip"
	"strings"
	"testing"
)

func TestPolicyMapValue(t *testing.T) {
	policy := Policy{
		XDPDropPct:   10,
		TCDropPct:    2.5,
		TCCorruptPct: 1,
		BlockedSrc:   netip.MustParseAddr("192.0.2.10"),
		BlockedDst:   netip.MustParseAddr("198.51.100.20"),
	}

	value, err := policy.mapValue()
	if err != nil {
		t.Fatal(err)
	}
	if len(value) != policyMapValueSize {
		t.Fatalf("map value length = %d, want %d", len(value), policyMapValueSize)
	}

	if got := binary.NativeEndian.Uint32(value[0:4]); got != 1000 {
		t.Errorf("XDP drop = %d, want 1000", got)
	}
	if got := binary.NativeEndian.Uint32(value[4:8]); got != 250 {
		t.Errorf("TC drop = %d, want 250", got)
	}
	if got := binary.NativeEndian.Uint32(value[8:12]); got != 100 {
		t.Errorf("TC corrupt = %d, want 100", got)
	}
	if want := []byte{192, 0, 2, 10}; !bytes.Equal(value[12:16], want) {
		t.Errorf("blocked source = %v, want %v", value[12:16], want)
	}
	if want := []byte{198, 51, 100, 20}; !bytes.Equal(value[16:20], want) {
		t.Errorf("blocked destination = %v, want %v", value[16:20], want)
	}
}

func TestPolicyMapValueRoundsToPerTenThousand(t *testing.T) {
	value, err := (Policy{XDPDropPct: 0.125}).mapValue()
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.NativeEndian.Uint32(value[0:4]); got != 13 {
		t.Fatalf("XDP drop = %d, want 13", got)
	}
}

func TestPolicyRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
		want   string
	}{
		{
			name:   "negative XDP drop",
			policy: Policy{XDPDropPct: -1},
			want:   "XDP drop percentage",
		},
		{
			name:   "TC drop over one hundred",
			policy: Policy{TCDropPct: 100.01},
			want:   "TC drop percentage",
		},
		{
			name:   "NaN corrupt rate",
			policy: Policy{TCCorruptPct: math.NaN()},
			want:   "TC corrupt percentage",
		},
		{
			name:   "infinite XDP drop",
			policy: Policy{XDPDropPct: math.Inf(1)},
			want:   "XDP drop percentage",
		},
		{
			name:   "IPv6 source",
			policy: Policy{BlockedSrc: netip.MustParseAddr("2001:db8::1")},
			want:   "blocked source must be an IPv4 address",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.policy.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, test.want)
			}
		})
	}
}
