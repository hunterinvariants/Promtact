package tokenring

import (
	"reflect"
	"slices"
	"testing"
)

func TestNewRejectsTooFewNodes(t *testing.T) {
	if _, err := New(1); err == nil {
		t.Fatal("New(1) succeeded, want an error")
	}
}

func TestTokenPassesInRingOrder(t *testing.T) {
	ring, err := New(3)
	if err != nil {
		t.Fatalf("New(3) failed: %v", err)
	}

	requireHolders(t, ring, []uint32{1})

	ring.Advance(1)
	requireHolders(t, ring, nil)

	first := ring.TakeOutbound(1)
	if got, want := first, []Message{{
		From:     1,
		To:       2,
		Sequence: 1,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first transfer = %#v, want %#v", got, want)
	}

	if got := ring.TakeOutbound(1); got != nil {
		t.Fatalf("second drain = %#v, want nil", got)
	}

	ring.Receive(2, first[0])
	requireHolders(t, ring, []uint32{2})

	ring.Advance(2)
	second := ring.TakeOutbound(2)
	if got, want := second, []Message{{
		From:     2,
		To:       3,
		Sequence: 2,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second transfer = %#v, want %#v", got, want)
	}

	ring.Receive(3, second[0])
	requireHolders(t, ring, []uint32{3})

	ring.Advance(3)
	third := ring.TakeOutbound(3)
	if got, want := third, []Message{{
		From:     3,
		To:       1,
		Sequence: 3,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("third transfer = %#v, want %#v", got, want)
	}

	ring.Receive(1, third[0])
	requireHolders(t, ring, []uint32{1})

	if got, want := ring.Passes(), uint64(3); got != want {
		t.Fatalf("Passes() = %d, want %d", got, want)
	}
}

func TestReceiveRejectsInvalidAndStaleMessages(t *testing.T) {
	ring, err := New(3)
	if err != nil {
		t.Fatalf("New(3) failed: %v", err)
	}

	ring.Receive(2, Message{
		From:     99,
		To:       2,
		Sequence: 10,
	})
	requireHolders(t, ring, []uint32{1})

	ring.Receive(2, Message{
		From:     1,
		To:       3,
		Sequence: 10,
	})
	requireHolders(t, ring, []uint32{1})

	ring.Advance(1)
	message := ring.TakeOutbound(1)[0]
	ring.Receive(2, message)
	requireHolders(t, ring, []uint32{2})

	ring.Receive(2, message)
	requireHolders(t, ring, []uint32{2})
}

func TestNodeIDsReturnsIndependentCopy(t *testing.T) {
	ring, err := New(3)
	if err != nil {
		t.Fatalf("New(3) failed: %v", err)
	}

	ids := ring.NodeIDs()
	ids[0] = 99

	if got, want := ring.NodeIDs(), []uint32{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("NodeIDs() = %v, want %v", got, want)
	}
}

func requireHolders(t *testing.T, ring *Ring, want []uint32) {
	t.Helper()

	if got := ring.HolderIDs(); !slices.Equal(got, want) {
		t.Fatalf("HolderIDs() = %v, want %v", got, want)
	}
}
