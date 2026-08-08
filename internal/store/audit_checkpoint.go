package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Retention checkpoints: telling deletion-by-policy apart from deletion-by-attacker.
//
// A hash chain and a retention policy contradict each other. Every record links
// to the one before it, so removing the oldest records leaves the first survivor
// pointing at a hash that no longer exists, and verification reports BROKEN -
// on a deployment where nothing was attacked and everything worked as
// configured. This was live: a permanent false alarm on the one signal that is
// supposed to mean something.
//
// The fix is a checkpoint written at the moment of pruning, recording what was
// removed and, critically, the hash of the last removed record. Verification
// then starts from the checkpoint instead of from nothing.
//
// The part that makes this a control rather than a note-to-self is that the
// checkpoint has to *link*. Its hash must equal the first surviving record's
// PrevHash. An attacker who deletes a record from the middle and writes a
// checkpoint claiming retention did it produces a checkpoint that does not
// join up, and the chain stays broken. A checkpoint that merely asserted "some
// records were removed here, trust me" would hand an attacker the erasure this
// whole mechanism exists to prevent.
//
// It is additionally HMAC-signed with the same key as the chain anchor, so
// writing a plausible checkpoint requires the key rather than table access.

// AuditCheckpoint records a retention prune.
type AuditCheckpoint struct {
	// PrunedThrough is the chain index of the last removed record, and Hash is
	// that record's hash - the value the first surviving record links back to.
	PrunedThrough int    `json:"pruned_through"`
	Hash          string `json:"hash"`

	RemovedCount int       `json:"removed_count"`
	CreatedAt    time.Time `json:"created_at"`

	// Signature covers the fields above. Without it, anyone able to write to
	// the table could explain away a deletion after the fact.
	Signature string `json:"signature,omitempty"`
}

func checkpointSignature(checkpoint AuditCheckpoint) string {
	key := auditChainAnchorKey()
	if len(key) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("promtact-retention-checkpoint-v1"))
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d", checkpoint.PrunedThrough)))
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write([]byte(strings.ToLower(strings.TrimSpace(checkpoint.Hash))))
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d", checkpoint.RemovedCount)))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignatureValid reports whether the checkpoint carries a signature this
// deployment's key produced.
//
// A deployment with no anchor key configured has no signatures to check; that
// is reported by the caller as unsigned rather than as a failure, exactly as
// the anchor itself already is.
func (c AuditCheckpoint) SignatureValid() bool {
	expected := checkpointSignature(c)
	if expected == "" {
		return false
	}
	return hmac.Equal([]byte(expected), []byte(strings.TrimSpace(c.Signature)))
}

// recordRetentionCheckpointLocked is called by retention when audit records are
// about to be dropped. removed must be the records leaving the store.
func (s *Store) recordRetentionCheckpointLocked(removed []auditRef) {
	if len(removed) == 0 {
		return
	}
	// The last removed record by chain index is the one the survivors link back
	// to. Taking the maximum rather than the final slice element means this
	// stays correct even if the retained set is not a clean prefix.
	last := removed[0]
	for _, candidate := range removed[1:] {
		if candidate.index > last.index {
			last = candidate
		}
	}
	if strings.TrimSpace(last.hash) == "" {
		return
	}

	checkpoint := AuditCheckpoint{
		PrunedThrough: last.index,
		Hash:          last.hash,
		RemovedCount:  len(removed),
		CreatedAt:     time.Now().UTC(),
	}
	checkpoint.Signature = checkpointSignature(checkpoint)

	// Supersede rather than accumulate: only the most recent prune boundary is
	// needed to verify, and keeping every one would grow without bound in the
	// one table that must never be pruned.
	if existing, ok := s.latestCheckpointLocked(); ok && existing.PrunedThrough >= checkpoint.PrunedThrough {
		return
	}
	s.checkpoint = &checkpoint
	s.persistCheckpointLocked(checkpoint)
}

type auditRef struct {
	index int
	hash  string
}

func (s *Store) latestCheckpointLocked() (AuditCheckpoint, bool) {
	if s.checkpoint == nil {
		return AuditCheckpoint{}, false
	}
	return *s.checkpoint, true
}

// LatestAuditCheckpoint returns the current retention boundary, if any.
func (s *Store) LatestAuditCheckpoint() (AuditCheckpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latestCheckpointLocked()
}

func (s *Store) persistCheckpointLocked(checkpoint AuditCheckpoint) {
	if s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.ExecContext(ctx, `
INSERT INTO promtact_audit_checkpoints (id, pruned_through, hash, removed_count, created_at, signature)
VALUES (1, $1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
  pruned_through = EXCLUDED.pruned_through,
  hash = EXCLUDED.hash,
  removed_count = promtact_audit_checkpoints.removed_count + EXCLUDED.removed_count,
  created_at = EXCLUDED.created_at,
  signature = EXCLUDED.signature
WHERE promtact_audit_checkpoints.pruned_through < EXCLUDED.pruned_through`,
		checkpoint.PrunedThrough, checkpoint.Hash, checkpoint.RemovedCount,
		checkpoint.CreatedAt, checkpoint.Signature)
	if err != nil {
		s.lastErr = err.Error()
	}
}

func (s *Store) loadCheckpointLocked(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	var checkpoint AuditCheckpoint
	err := s.db.QueryRowContext(ctx, `
SELECT pruned_through, hash, removed_count, created_at, COALESCE(signature, '')
FROM promtact_audit_checkpoints WHERE id = 1`).
		Scan(&checkpoint.PrunedThrough, &checkpoint.Hash, &checkpoint.RemovedCount,
			&checkpoint.CreatedAt, &checkpoint.Signature)
	if err != nil {
		// No row is the normal case: nothing has been pruned yet.
		return nil
	}
	s.checkpoint = &checkpoint
	return nil
}

// chainSeed returns the hash verification should start from, and whether that
// seed came from a checkpoint.
//
// With no checkpoint the chain must start from nothing, which is what an
// untouched deployment looks like. With one, it starts from the last pruned
// record - and if the survivors do not actually link to it, the chain is still
// broken, which is the entire point.
func (s *Store) chainSeedLocked() (string, bool) {
	checkpoint, ok := s.latestCheckpointLocked()
	if !ok {
		return "", false
	}
	// An unsigned or badly signed checkpoint explains nothing. Refusing to seed
	// from it leaves the chain reported as broken, which is the safe direction:
	// the alternative is that writing a row to this table erases history.
	if !checkpoint.SignatureValid() {
		return "", false
	}
	return checkpoint.Hash, true
}
