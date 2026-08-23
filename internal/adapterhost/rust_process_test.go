// Licensed under the Apache License, Version 2.0.

package adapterhost

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/dst"
	"github.com/hunterinvariants/promtact/internal/adapterproto"
)

const rustAdapterEnvironment = "PROMTACT_RUST_ADAPTER"

type rustCampaignResult struct {
	trace   string
	drops   map[string]int
	pending int
	now     uint64
}

func TestRustProcessAdapterCampaignIsReproducible(t *testing.T) {
	binary := os.Getenv(rustAdapterEnvironment)
	if binary == "" {
		t.Skipf("%s is not set", rustAdapterEnvironment)
	}

	absolute, err := filepath.Abs(binary)
	if err != nil {
		t.Fatalf("resolve Rust adapter path: %v", err)
	}
	if info, err := os.Stat(absolute); err != nil {
		t.Fatalf("stat Rust adapter: %v", err)
	} else if info.IsDir() {
		t.Fatalf("Rust adapter path %q is a directory", absolute)
	}

	const seed int64 = 0x71e5

	first := runRustProcessCampaign(t, absolute, seed)
	second := runRustProcessCampaign(t, absolute, seed)

	if !reflect.DeepEqual(first, second) {
		t.Fatalf(
			"identical Rust campaigns diverged:\nfirst:  %#v\nsecond: %#v",
			first,
			second,
		)
	}
	if first.trace == "" {
		t.Fatal("Rust campaign returned an empty trace hash")
	}
	if first.now != 200 {
		t.Fatalf("Rust campaign ended at step %d, want 200", first.now)
	}

	dropped := 0
	for _, count := range first.drops {
		dropped += count
	}
	if dropped == 0 {
		t.Fatal("Rust campaign completed without activating its fault")
	}

	t.Logf(
		"Rust campaign seed=%d step=%d pending=%d drops=%v trace=%s",
		seed,
		first.now,
		first.pending,
		first.drops,
		first.trace,
	)
}

func runRustProcessCampaign(
	t *testing.T,
	binary string,
	seed int64,
) rustCampaignResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()

	process, err := StartProcess(
		ctx,
		ProcessOptions{
			Command: binary,
			Args:    []string{"--nodes", "3"},
		},
		seed,
	)
	if err != nil {
		t.Fatalf("start Rust adapter: %v", err)
	}

	closed := false
	defer func() {
		if !closed {
			_ = process.Close()
		}
	}()

	runner, err := NewRunner(
		dst.Config{
			Seed:     seed,
			MaxDelay: 3,
		},
		process.Session(),
	)
	if err != nil {
		t.Fatalf("create Rust adapter runner: %v", err)
	}

	partition := dst.During(
		20,
		60,
		dst.Split(
			[]uint32{1},
			[]uint32{2, 3},
		),
	)
	runner.Inject(partition)

	if err := runner.RunChecked(200); err != nil {
		t.Fatalf("run Rust adapter campaign: %v", err)
	}

	result := rustCampaignResult{
		trace:   runner.TraceHash(),
		drops:   runner.InjectedDrops(),
		pending: runner.Pending(),
		now:     runner.Now(),
	}

	if err := process.Close(); err != nil {
		t.Fatalf("close Rust adapter: %v", err)
	}
	closed = true

	return result
}

func TestRustProcessAdapterReportsNegativeControl(t *testing.T) {
	binary := os.Getenv(rustAdapterEnvironment)
	if binary == "" {
		t.Skipf("%s is not set", rustAdapterEnvironment)
	}

	absolute, err := filepath.Abs(binary)
	if err != nil {
		t.Fatalf("resolve Rust adapter path: %v", err)
	}

	first := runRustNegativeControl(t, absolute)
	second := runRustNegativeControl(t, absolute)

	if !reflect.DeepEqual(first, second) {
		t.Fatalf(
			"Rust negative controls diverged:\nfirst:  %#v\nsecond: %#v",
			first,
			second,
		)
	}

	if first.Invariant != "at-most-one-token" {
		t.Fatalf(
			"invariant = %q, want %q",
			first.Invariant,
			"at-most-one-token",
		)
	}
	if first.Detail != "token held by nodes [1, 2]" {
		t.Fatalf(
			"detail = %q, want duplicate holders",
			first.Detail,
		)
	}

	t.Logf(
		"Rust negative control invariant=%q detail=%q",
		first.Invariant,
		first.Detail,
	)
}

func runRustNegativeControl(
	t *testing.T,
	binary string,
) adapterproto.Violation {
	t.Helper()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()

	process, err := StartProcess(
		ctx,
		ProcessOptions{
			Command: binary,
			Args:    []string{"--nodes", "3"},
		},
		0x5eed,
	)
	if err != nil {
		t.Fatalf("start Rust adapter: %v", err)
	}

	closed := false
	defer func() {
		if !closed {
			_ = process.Close()
		}
	}()

	sequence := uint64(1<<40 | 9)

	message := adapterproto.Message{
		From:    1,
		To:      2,
		Kind:    1,
		Value:   sequence<<32 | uint64(1)<<16 | uint64(2),
		Payload: sequenceBytes(sequence),
	}

	if err := process.Session().Deliver(2, message); err != nil {
		t.Fatalf("deliver negative-control token: %v", err)
	}

	violation, err := process.Session().Check()
	if err != nil {
		t.Fatalf("check Rust negative control: %v", err)
	}
	if violation == nil {
		t.Fatal("Rust negative control returned no violation")
	}

	result := *violation

	if err := process.Close(); err != nil {
		t.Fatalf("close Rust adapter: %v", err)
	}
	closed = true

	return result
}

func sequenceBytes(sequence uint64) []byte {
	return []byte{
		byte(sequence >> 56),
		byte(sequence >> 48),
		byte(sequence >> 40),
		byte(sequence >> 32),
		byte(sequence >> 24),
		byte(sequence >> 16),
		byte(sequence >> 8),
		byte(sequence),
	}
}

func TestRustProcessAdapterMatchesGoReferenceTrace(t *testing.T) {
	binary := os.Getenv(rustAdapterEnvironment)
	if binary == "" {
		t.Skipf("%s is not set", rustAdapterEnvironment)
	}

	absolute, err := filepath.Abs(binary)
	if err != nil {
		t.Fatalf("resolve Rust adapter path: %v", err)
	}

	// This is the result of the checked-in Go token-ring consumer's
	// TestTimedPartitionIsDeterministicAndNonVacuous campaign.
	const (
		seed        int64  = 0x71e5
		wantTrace          = "77a8aabf3cbd3ad3ee4b4543d36564c02a4888940d5f05c048db37eee56bada8"
		wantFault          = "split[1]|[2 3]@[20,60)"
		wantDrops          = 1
		wantPending        = 0
		wantStep    uint64 = 200
	)

	result := runRustProcessCampaign(t, absolute, seed)

	if result.trace != wantTrace {
		t.Fatalf(
			"Rust trace = %q, want Go reference %q",
			result.trace,
			wantTrace,
		)
	}
	if result.pending != wantPending {
		t.Fatalf(
			"Rust pending = %d, want Go reference %d",
			result.pending,
			wantPending,
		)
	}
	if result.now != wantStep {
		t.Fatalf(
			"Rust step = %d, want Go reference %d",
			result.now,
			wantStep,
		)
	}
	if len(result.drops) != 1 {
		t.Fatalf(
			"Rust fault counters = %v, want one Go reference counter",
			result.drops,
		)
	}
	if got := result.drops[wantFault]; got != wantDrops {
		t.Fatalf(
			"Rust fault %q drops = %d, want Go reference %d",
			wantFault,
			got,
			wantDrops,
		)
	}
}
