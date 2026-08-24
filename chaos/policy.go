package chaos

import (
	"encoding/binary"
	"fmt"
	"math"
	"net/netip"
)

const policyMapValueSize = 20

// Policy controls the BPF packet actions. Netem delay and loss are separate
// Plan fields because they are implemented by a different kernel facility.
type Policy struct {
	XDPDropPct   float64
	TCDropPct    float64
	TCCorruptPct float64
	BlockedSrc   netip.Addr
	BlockedDst   netip.Addr
}

func (p Policy) Validate() error {
	rates := []struct {
		name       string
		percentage float64
	}{
		{name: "XDP drop", percentage: p.XDPDropPct},
		{name: "TC drop", percentage: p.TCDropPct},
		{name: "TC corrupt", percentage: p.TCCorruptPct},
	}
	for _, rate := range rates {
		if _, err := policyRate(rate.name, rate.percentage); err != nil {
			return err
		}
	}
	if err := validatePolicyAddress("blocked source", p.BlockedSrc); err != nil {
		return err
	}
	return validatePolicyAddress("blocked destination", p.BlockedDst)
}

func (p Policy) mapValue() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	xdpDrop, _ := policyRate("XDP drop", p.XDPDropPct)
	tcDrop, _ := policyRate("TC drop", p.TCDropPct)
	tcCorrupt, _ := policyRate("TC corrupt", p.TCCorruptPct)

	value := make([]byte, policyMapValueSize)
	binary.NativeEndian.PutUint32(value[0:4], xdpDrop)
	binary.NativeEndian.PutUint32(value[4:8], tcDrop)
	binary.NativeEndian.PutUint32(value[8:12], tcCorrupt)
	copyPolicyAddress(value[12:16], p.BlockedSrc)
	copyPolicyAddress(value[16:20], p.BlockedDst)
	return value, nil
}

func policyRate(name string, percentage float64) (uint32, error) {
	if math.IsNaN(percentage) || math.IsInf(percentage, 0) ||
		percentage < 0 || percentage > 100 {
		return 0, fmt.Errorf("chaos: %s percentage outside safety bounds", name)
	}
	return uint32(math.Round(percentage * 100)), nil
}

func validatePolicyAddress(name string, address netip.Addr) error {
	if address.IsValid() && !address.Is4() {
		return fmt.Errorf("chaos: %s must be an IPv4 address", name)
	}
	return nil
}

func copyPolicyAddress(destination []byte, address netip.Addr) {
	if !address.IsValid() {
		return
	}
	ip := address.As4()
	copy(destination, ip[:])
}
