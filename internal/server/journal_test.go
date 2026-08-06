package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestJournalAppendAndDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	journal := newDecisionJournal(path, 100)

	for i := 0; i < 3; i++ {
		if err := journal.Append(journalKindAlerts, "acme", "db down", map[string]any{"i": i}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if journal.Depth() != 3 {
		t.Fatalf("expected depth 3, got %d", journal.Depth())
	}

	seen := []int{}
	applied, err := journal.Drain(func(entry journalEntry) error {
		if entry.Kind != journalKindAlerts || entry.Tenant != "acme" {
			t.Errorf("unexpected entry: %+v", entry)
		}
		var payload struct {
			I int `json:"i"`
		}
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			return err
		}
		seen = append(seen, payload.I)
		return nil
	})
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if applied != 3 || len(seen) != 3 {
		t.Fatalf("expected 3 reconciled, got applied=%d seen=%v", applied, seen)
	}
	// Order must be preserved: an audit trail replayed out of order is not the
	// same trail.
	for i, got := range seen {
		if got != i {
			t.Fatalf("entries replayed out of order: %v", seen)
		}
	}
	if journal.Depth() != 0 {
		t.Fatalf("journal should be empty after a full drain, got %d", journal.Depth())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the journal file should be removed once fully reconciled")
	}
}

// A partial recovery must keep the unreconciled remainder instead of dropping
// it, and must not reapply what already landed.
func TestJournalDrainStopsOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	journal := newDecisionJournal(path, 100)
	for i := 0; i < 4; i++ {
		if err := journal.Append(journalKindActions, "acme", "", map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}

	calls := 0
	applied, err := journal.Drain(func(entry journalEntry) error {
		calls++
		if calls > 2 {
			return errors.New("still unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if applied != 2 {
		t.Fatalf("expected 2 reconciled before the failure, got %d", applied)
	}
	if journal.Depth() != 2 {
		t.Fatalf("expected 2 entries kept, got %d", journal.Depth())
	}

	rest, err := journal.Drain(func(entry journalEntry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if rest != 2 || journal.Depth() != 0 {
		t.Fatalf("the remainder should reconcile later: applied=%d depth=%d", rest, journal.Depth())
	}
}

// The journal refuses new entries once full rather than rotating: discarding the
// oldest security records would be exactly the loss it exists to prevent.
func TestJournalRefusesWhenFull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	journal := newDecisionJournal(path, 2)

	for i := 0; i < 2; i++ {
		if err := journal.Append(journalKindAlerts, "acme", "", map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Append(journalKindAlerts, "acme", "", map[string]any{"i": 99}); err == nil {
		t.Fatal("a full journal must refuse further entries")
	}
	if journal.Dropped() != 1 {
		t.Fatalf("the refusal must be counted, got %d", journal.Dropped())
	}
	if journal.Depth() != 2 {
		t.Fatalf("depth should stay at the cap, got %d", journal.Depth())
	}
}

// Restarting must not lose the backlog: depth is recovered from disk.
func TestJournalRecoversDepthOnRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	first := newDecisionJournal(path, 100)
	for i := 0; i < 5; i++ {
		if err := first.Append(journalKindAlerts, "acme", "", map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}

	reopened := newDecisionJournal(path, 100)
	if reopened.Depth() != 5 {
		t.Fatalf("expected the backlog to survive a restart, got %d", reopened.Depth())
	}
}

func TestJournalDisabledWithoutPath(t *testing.T) {
	journal := newDecisionJournal("", 10)
	if journal.enabled() {
		t.Fatal("a journal without a path must be disabled")
	}
	if err := journal.Append(journalKindAlerts, "acme", "", map[string]any{}); err == nil {
		t.Fatal("appending to a disabled journal must fail explicitly")
	}
	if journal.Depth() != 0 {
		t.Fatal("a disabled journal has no depth")
	}
}
