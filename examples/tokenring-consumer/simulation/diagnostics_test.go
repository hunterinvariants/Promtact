package simulation

import (
	"errors"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/dst"
	"github.com/hunterinvariants/promtact/examples/tokenring-consumer/tokenring"
)

// duplicateCluster models a faulty integration that emits one token transfer
// to two destinations. The underlying protocol remains unchanged.
type duplicateCluster struct {
	*cluster
	duplicated bool
}

func (c *duplicateCluster) Drain(
	node uint32,
	destination []tokenring.Message,
) []tokenring.Message {
	start := len(destination)
	destination = c.cluster.Drain(node, destination)

	if c.duplicated || len(destination) == start {
		return destination
	}

	duplicate := destination[start]

	for _, candidate := range c.nodes {
		if candidate == duplicate.From || candidate == duplicate.To {
			continue
		}

		duplicate.To = candidate
		destination = append(destination, duplicate)
		c.duplicated = true
		break
	}

	return destination
}

func runDuplicateTransfer() error {
	base, err := newCluster(3)
	if err != nil {
		return err
	}

	faulty := &duplicateCluster{
		cluster: base,
	}

	engine := dst.New(
		dst.Config{
			Seed: 0x5eed,
		},
		faulty,
		wire{},
	)

	engine.Watch(atMostOneToken(base.ring))
	return engine.RunChecked(5)
}

func TestViolationDiagnosticIsReproducible(t *testing.T) {
	firstErr := runDuplicateTransfer()
	if firstErr == nil {
		t.Fatal("faulty transfer completed without an invariant violation")
	}

	secondErr := runDuplicateTransfer()
	if secondErr == nil {
		t.Fatal("repeated faulty transfer completed without an invariant violation")
	}

	var first *dst.Violation
	if !errors.As(firstErr, &first) {
		t.Fatalf("first error has type %T, want *dst.Violation", firstErr)
	}

	var second *dst.Violation
	if !errors.As(secondErr, &second) {
		t.Fatalf("second error has type %T, want *dst.Violation", secondErr)
	}

	if got, want := first.Invariant, "at-most-one-token"; got != want {
		t.Fatalf("invariant = %q, want %q", got, want)
	}

	if got, want := first.Step, uint64(1); got != want {
		t.Fatalf("violation step = %d, want %d", got, want)
	}

	if first.Trace == "" {
		t.Fatal("violation has an empty trace hash")
	}

	if first.Trace != second.Trace {
		t.Fatalf(
			"violation traces differ: first=%s second=%s",
			first.Trace,
			second.Trace,
		)
	}

	if first.Error() != second.Error() {
		t.Fatalf(
			"violation diagnostics differ:\nfirst:  %s\nsecond: %s",
			first,
			second,
		)
	}

	for _, fragment := range []string{
		`invariant "at-most-one-token"`,
		"step 1",
		"trace ",
		"token held by nodes [2 3]",
	} {
		if !strings.Contains(first.Error(), fragment) {
			t.Fatalf(
				"diagnostic %q does not contain %q",
				first,
				fragment,
			)
		}
	}

	t.Logf("diagnostic: %v", first)
}

var _ dst.Cluster[tokenring.Message] = (*duplicateCluster)(nil)
