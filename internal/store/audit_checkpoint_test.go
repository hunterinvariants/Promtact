package store

import (
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// Retention checkpoints have to do two opposite things, and only testing the
// first is how this becomes a way to erase history:
//
//   - a chain shortened by retention must verify
//   - a chain shortened by anyone else must not

func checkpointStore(t *testing.T) *Store {
	t.Helper()
	// The checkpoint is signed with the chain anchor key; without one there is
	// nothing to verify and the seed is refused by design.
	t.Setenv("PROMTACT_AUDIT_HMAC_SECRET", "test-anchor-key-for-checkpoints")
	return New()
}

func addAudits(t *testing.T, s *Store, count int, age time.Duration) {
	t.Helper()
	for i := 0; i < count; i++ {
		err := s.AddAudit(domain.AuditEvent{
			ID:        "audit-" + time.Now().UTC().Format("150405.000000000"),
			Timestamp: time.Now().UTC().Add(-age),
			Action:    "test.event",
			Outcome:   "ok",
		})
		if err != nil {
			t.Fatalf("adding audit %d: %v", i, err)
		}
		time.Sleep(time.Microsecond)
	}
}

// The false alarm this fixes: retention prunes, and the chain reports BROKEN
// on a deployment where nothing was attacked.
func TestRetentionPruningKeepsTheChainValid(t *testing.T) {
	s := checkpointStore(t)

	addAudits(t, s, 5, 48*time.Hour) // old enough to be pruned
	addAudits(t, s, 5, 0)            // recent

	before := s.AuditChain()
	if !before.Valid || before.Total != 10 {
		t.Fatalf("precondition: chain should start valid with 10 records, got valid=%v total=%d",
			before.Valid, before.Total)
	}

	if err := s.SetRetention(24 * time.Hour); err != nil {
		t.Fatalf("applying retention: %v", err)
	}

	after := s.AuditChain()
	if after.Total != 5 {
		t.Fatalf("retention should have removed the 5 old records, %d remain", after.Total)
	}
	if !after.Valid {
		t.Fatalf("the chain reports BROKEN after ordinary retention - this is the false alarm "+
			"the checkpoint exists to remove (pruned=%d through=%d)",
			after.PrunedRecords, after.PrunedThrough)
	}
	if after.PrunedRecords != 5 {
		t.Errorf("the snapshot reports %d pruned records, want 5 - a reader told the chain is "+
			"intact must also be told it is shorter than its history", after.PrunedRecords)
	}
}

// The direction that matters more. A checkpoint must not be a way to explain
// away a deletion that retention did not make.
func TestAForgedCheckpointDoesNotRepairADeletedRecord(t *testing.T) {
	s := checkpointStore(t)
	addAudits(t, s, 6, 0)

	if chain := s.AuditChain(); !chain.Valid {
		t.Fatalf("precondition: chain should be valid, got %+v", chain)
	}

	// Delete a record from the middle, as an attacker with database access
	// would, and write a checkpoint claiming retention did it.
	s.mu.Lock()
	removed := s.audits[2]
	s.audits = append(append([]domain.AuditEvent(nil), s.audits[:2]...), s.audits[3:]...)
	forged := AuditCheckpoint{
		PrunedThrough: removed.ChainIndex,
		Hash:          removed.Hash,
		RemovedCount:  1,
		CreatedAt:     time.Now().UTC(),
	}
	// Correctly signed: the attacker is assumed to have the key, which is the
	// harder case. The chain must still not verify, because the surviving
	// records do not link across the hole.
	forged.Signature = checkpointSignature(forged)
	s.checkpoint = &forged
	s.rebuildAuditChainLocked()
	s.mu.Unlock()

	if chain := s.AuditChain(); chain.Valid {
		t.Fatal("a record was deleted from the middle of the chain and a signed checkpoint " +
			"made it verify - the checkpoint is an erasure tool, not a control")
	}
}

// The strict version of the test above.
//
// That one deletes from the middle and points the checkpoint at the deleted
// record, so verification fails at the very first survivor - whose PrevHash is
// empty and cannot match the seed. It passes, but never reaches the hole it
// claims to be about, so it would keep passing even if a mid-chain gap were
// silently tolerated.
//
// Here retention prunes legitimately first, so the seed is correct and the
// early records link cleanly. Only then is a record removed from the middle of
// what remains. The break now has to be found at the hole itself, which is the
// property being claimed.
func TestADeletionAfterALegitimatePruneIsStillDetected(t *testing.T) {
	s := checkpointStore(t)
	addAudits(t, s, 3, 48*time.Hour)
	addAudits(t, s, 5, 0)

	if err := s.SetRetention(24 * time.Hour); err != nil {
		t.Fatalf("retention: %v", err)
	}
	chain := s.AuditChain()
	if !chain.Valid || chain.Total != 5 {
		t.Fatalf("precondition: expected a valid 5-record chain after pruning, got valid=%v total=%d",
			chain.Valid, chain.Total)
	}

	// Now remove one from the middle of the surviving records, leaving the
	// checkpoint and its signature untouched and correct.
	s.mu.Lock()
	s.audits = append(append([]domain.AuditEvent(nil), s.audits[:2]...), s.audits[3:]...)
	s.rebuildAuditChainLocked()
	s.mu.Unlock()

	if after := s.AuditChain(); after.Valid {
		t.Fatal("a record removed from the middle of the retained chain still verified - " +
			"the checkpoint seed is masking gaps rather than only explaining the pruned prefix")
	}
}

// An unsigned checkpoint explains nothing. Seeding from it would mean that
// writing a row to a table rewrites history.
func TestAnUnsignedCheckpointIsNotTrusted(t *testing.T) {
	s := checkpointStore(t)
	addAudits(t, s, 4, 48*time.Hour)
	addAudits(t, s, 2, 0)

	if err := s.SetRetention(24 * time.Hour); err != nil {
		t.Fatalf("retention: %v", err)
	}
	if chain := s.AuditChain(); !chain.Valid {
		t.Fatalf("precondition: retention should leave a valid chain, got %+v", chain)
	}

	s.mu.Lock()
	stripped := *s.checkpoint
	stripped.Signature = ""
	s.checkpoint = &stripped
	s.rebuildAuditChainLocked()
	s.mu.Unlock()

	if chain := s.AuditChain(); chain.Valid {
		t.Fatal("an unsigned checkpoint was accepted as an explanation for missing records")
	}
}

// A checkpoint whose signature was tampered with is the same case, and is worth
// its own test because it is the one an attacker without the key produces.
func TestATamperedCheckpointSignatureIsRejected(t *testing.T) {
	s := checkpointStore(t)
	addAudits(t, s, 4, 48*time.Hour)
	addAudits(t, s, 2, 0)
	if err := s.SetRetention(24 * time.Hour); err != nil {
		t.Fatalf("retention: %v", err)
	}

	s.mu.Lock()
	tampered := *s.checkpoint
	tampered.RemovedCount = 999 // covered by the signature
	s.checkpoint = &tampered
	s.rebuildAuditChainLocked()
	s.mu.Unlock()

	if tampered.SignatureValid() {
		t.Fatal("the signature still validates after a covered field changed")
	}
	if chain := s.AuditChain(); chain.Valid {
		t.Fatal("a checkpoint with a broken signature was still used to seed the chain")
	}
}

// The restart is where the false alarm actually appears, and none of the tests
// above reach it.
//
// enforceRetentionLocked replaces the audit slice but does not re-validate the
// chain, so a running process keeps reporting valid until something rebuilds -
// which happens on load. The bug therefore shows up at the next start, not at
// the prune, and a test that only prunes in-process cannot see it. Confirmed
// against the pre-checkpoint binary: valid before restart, invalid after.
//
// That also means the checkpoint has to survive persistence, which is a
// separate thing from being computed correctly.
func TestTheCheckpointSurvivesASnapshotRoundTrip(t *testing.T) {
	s := checkpointStore(t)
	addAudits(t, s, 4, 48*time.Hour)
	addAudits(t, s, 3, 0)

	if err := s.SetRetention(24 * time.Hour); err != nil {
		t.Fatalf("retention: %v", err)
	}
	before, ok := s.LatestAuditCheckpoint()
	if !ok {
		t.Fatal("no checkpoint was written by the prune")
	}

	snap := s.ExportSnapshot()
	if snap.Checkpoint == nil {
		t.Fatal("the snapshot carries no checkpoint, so a restart loses the retention " +
			"boundary and the chain reports BROKEN at the next start")
	}

	// Reload exactly as a restart would, through the same rebuild.
	reloaded := New()
	reloaded.audits = append([]domain.AuditEvent(nil), snap.Audits...)
	reloaded.checkpoint = snap.Checkpoint
	reloaded.rebuildAuditChainLocked()

	chain := reloaded.AuditChain()
	if !chain.Valid {
		t.Fatalf("after reloading a pruned chain the verification reports BROKEN - "+
			"this is the false alarm, deferred to the next restart (%+v)", chain)
	}
	if after, _ := reloaded.LatestAuditCheckpoint(); after.PrunedThrough != before.PrunedThrough {
		t.Errorf("the boundary changed across the round trip: %d then %d",
			before.PrunedThrough, after.PrunedThrough)
	}

	// And the control: the same reload without the checkpoint must fail, or the
	// test above proves only that the chain happens to verify anyway.
	without := New()
	without.audits = append([]domain.AuditEvent(nil), snap.Audits...)
	without.rebuildAuditChainLocked()
	if without.AuditChain().Valid {
		t.Fatal("a pruned chain reloaded without its checkpoint still verified, so this " +
			"test would pass whether or not the checkpoint does anything")
	}
}

// Retention runs repeatedly. The boundary has to move forward and the chain
// must stay valid across more than one prune, which is the case a single-shot
// test never reaches.
func TestRepeatedPruningMovesTheCheckpointForward(t *testing.T) {
	s := checkpointStore(t)
	addAudits(t, s, 4, 72*time.Hour)
	addAudits(t, s, 4, 36*time.Hour)
	addAudits(t, s, 4, 0)

	if err := s.SetRetention(48 * time.Hour); err != nil {
		t.Fatalf("first retention: %v", err)
	}
	first, ok := s.LatestAuditCheckpoint()
	if !ok {
		t.Fatal("no checkpoint after the first prune")
	}
	if chain := s.AuditChain(); !chain.Valid {
		t.Fatalf("chain invalid after the first prune: %+v", chain)
	}

	if err := s.SetRetention(24 * time.Hour); err != nil {
		t.Fatalf("second retention: %v", err)
	}
	second, ok := s.LatestAuditCheckpoint()
	if !ok {
		t.Fatal("no checkpoint after the second prune")
	}
	if second.PrunedThrough <= first.PrunedThrough {
		t.Fatalf("the boundary did not advance: %d then %d",
			first.PrunedThrough, second.PrunedThrough)
	}
	if chain := s.AuditChain(); !chain.Valid {
		t.Fatalf("chain invalid after the second prune: %+v", chain)
	}
	if chain := s.AuditChain(); chain.Total != 4 {
		t.Fatalf("expected 4 surviving records, got %d", chain.Total)
	}
}
