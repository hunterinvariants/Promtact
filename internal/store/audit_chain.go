package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

type AuditChainSnapshot struct {
	Total         int       `json:"total"`
	Linked        int       `json:"linked"`
	Unlinked      int       `json:"unlinked"`
	Head          string    `json:"head"`
	Previous      string    `json:"previous"`
	Valid         bool      `json:"valid"`
	Anchor        string    `json:"anchor,omitempty"`
	Anchored      bool      `json:"anchored,omitempty"`
	LastAuditID   string    `json:"last_audit_id,omitempty"`
	LastTimestamp time.Time `json:"last_timestamp,omitempty"`
	// TenantRecords is how many of the chain's records belong to the tenant
	// asking. It is a count and nothing more: validity belongs to the chain,
	// which is global.
	TenantRecords int `json:"tenant_records,omitempty"`

	// Retention removed records from the start of the chain, and verification
	// resumed from a signed checkpoint. Reported so a reader is told that the
	// chain is shorter than its history rather than being left to infer it -
	// "intact" over a chain whose first year was deleted, with no mention of
	// the deletion, would be an honest-looking answer to the wrong question.
	PrunedRecords int `json:"pruned_records,omitempty"`
	PrunedThrough int `json:"pruned_through,omitempty"`
}

// finalizeAuditChainSnapshot enforces coverage honesty: a chain that does not
// link every stored audit record is not fully valid, and the gap is reported
// explicitly via Unlinked so the snapshot never shows valid:true while audit
// records sit outside the anchored chain.
func finalizeAuditChainSnapshot(snap AuditChainSnapshot) AuditChainSnapshot {
	if snap.Linked < 0 {
		snap.Linked = 0
	}
	if snap.Linked > snap.Total {
		snap.Linked = snap.Total
	}
	snap.Unlinked = snap.Total - snap.Linked
	if snap.Unlinked > 0 {
		snap.Valid = false
	}
	return snap
}

func (s *Store) prepareAuditChainLocked(event domain.AuditEvent) domain.AuditEvent {
	index := len(s.audits) + 1
	event.ChainIndex = index
	event.PrevHash = s.auditChainHead
	event.Hash = auditEventHash(event, event.PrevHash)
	s.auditChainHead = event.Hash
	s.auditChainAnchor = auditChainAnchorValue(event.Hash, event.ChainIndex, true)
	s.auditChainValid = s.auditChainAnchor != ""
	return event
}

func (s *Store) rebuildAuditChainLocked() {
	head := ""
	total := len(s.audits)
	linked := 0
	valid := true

	// Start from the retention boundary rather than from nothing.
	//
	// Without this, pruning the oldest records leaves the first survivor
	// pointing at a hash that is no longer stored, previous is "", and the
	// chain reports BROKEN forever - on a deployment where retention did
	// exactly what it was configured to do. That false alarm was live, and a
	// permanently broken indicator is worse than none, because the one signal
	// that should mean something stops meaning anything.
	//
	// The seed is only used when the checkpoint is signed by this deployment's
	// key. An unsigned one explains nothing, and seeding from it would mean
	// that writing a row to a table erases history.
	previous, _ := s.chainSeedLocked()
	for _, audit := range s.audits {
		if strings.TrimSpace(audit.Hash) == "" {
			continue
		}
		if audit.ChainIndex == 0 {
			linked++
		} else {
			linked = audit.ChainIndex
		}
		if audit.PrevHash != previous {
			valid = false
		}
		if audit.Hash != auditEventHash(audit, audit.PrevHash) {
			valid = false
		}
		previous = audit.Hash
		head = audit.Hash
	}
	s.auditChainHead = head
	s.auditChainAnchor = auditChainAnchorValue(head, linked, valid)
	s.auditChainValid = valid && (total == 0 || s.auditChainAnchor != "")
}

func (s *Store) auditChainSnapshotLocked() AuditChainSnapshot {
	snap := AuditChainSnapshot{
		Total:    len(s.audits),
		Linked:   0,
		Head:     s.auditChainHead,
		Previous: "",
		Valid:    s.auditChainValid,
		Anchor:   s.auditChainAnchor,
		Anchored: s.auditChainAnchor != "",
	}
	for _, audit := range s.audits {
		if strings.TrimSpace(audit.Hash) == "" {
			continue
		}
		snap.Linked++
		if audit.Hash == s.auditChainHead {
			snap.LastAuditID = audit.ID
			snap.LastTimestamp = audit.Timestamp
			snap.Previous = audit.PrevHash
		}
	}
	if snap.Head == "" {
		snap.Valid = snap.Total == 0
		snap.Anchored = false
	}
	if checkpoint, ok := s.latestCheckpointLocked(); ok {
		snap.PrunedRecords = checkpoint.RemovedCount
		snap.PrunedThrough = checkpoint.PrunedThrough
	}
	return snap
}

// AuditChainForTenant reports the chain's integrity, plus how much of it
// belongs to this tenant.
//
// It does not validate a filtered subset, and this is the whole point. The
// chain is one sequence: every record links to the record before it across all
// tenants. Take some records out and the rest cannot link to each other, so a
// per-tenant validation reports a broken chain permanently, on a deployment
// where nothing has been touched. That is exactly what it did — a console
// telling a customer their audit trail was broken as its steady state, which
// is worse than showing nothing, because the one signal that should mean
// something now means nothing.
//
// Integrity is a property of the whole chain. A tenant gets that answer, plus
// a count of its own records.
func (s *Store) AuditChainForTenant(tenant string) AuditChainSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snap := finalizeAuditChainSnapshot(s.auditChainSnapshotLocked())
	snap.TenantRecords = s.countTenantAuditsLocked(tenant)
	return snap
}

func (s *Store) countTenantAuditsLocked(tenant string) int {
	tenant = tenantOrDefault(tenant)
	count := 0
	for _, audit := range s.audits {
		if sameTenant(audit.Tenant, tenant) {
			count++
		}
	}
	return count
}

func auditChainAnchorKey() []byte {
	if secret := strings.TrimSpace(os.Getenv("PROMTACT_AUDIT_HMAC_SECRET")); secret != "" {
		return []byte(secret)
	}
	// Fall back to deriving a domain-separated key from the session secret rather
	// than reusing it raw, so the audit-anchor HMAC and the session-signing HMAC
	// are cryptographically independent: learning the raw session secret no longer
	// lets an attacker forge a valid audit anchor over a tampered head.
	if secret := strings.TrimSpace(os.Getenv("PROMTACT_SESSION_SECRET")); secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte("promtact-audit-chain-anchor-v1"))
		return mac.Sum(nil)
	}
	return nil
}

func auditChainAnchorValue(head string, chainIndex int, valid bool) string {
	key := auditChainAnchorKey()
	if len(key) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(head))
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d", chainIndex)))
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write([]byte(fmt.Sprintf("%t", valid)))
	return hex.EncodeToString(mac.Sum(nil))
}

func auditEventHash(event domain.AuditEvent, prevHash string) string {
	builder := strings.Builder{}
	builder.WriteString(prevHash)
	builder.WriteByte('|')
	builder.WriteString(event.ID)
	builder.WriteByte('|')
	builder.WriteString(event.Timestamp.UTC().Format(time.RFC3339Nano))
	builder.WriteByte('|')
	builder.WriteString(event.Actor)
	builder.WriteByte('|')
	builder.WriteString(strings.Join(event.Roles, ","))
	builder.WriteByte('|')
	builder.WriteString(event.Action)
	builder.WriteByte('|')
	builder.WriteString(event.ResourceType)
	builder.WriteByte('|')
	builder.WriteString(event.ResourceID)
	builder.WriteByte('|')
	builder.WriteString(event.Outcome)
	builder.WriteByte('|')
	builder.WriteString(event.SourceIP)
	builder.WriteByte('|')
	builder.WriteString(event.UserAgent)
	builder.WriteByte('|')
	builder.WriteString(canonicalMetadata(event.Metadata))
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

func canonicalMetadata(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, metadata[key]))
	}
	return strings.Join(parts, ";")
}
