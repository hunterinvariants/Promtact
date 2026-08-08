package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// A tenant must not be told its audit trail is broken because other tenants
// exist.
//
// The chain is one sequence: every record links to the one before it, across
// all tenants. Validating a tenant-filtered subset therefore reports a break at
// every record that belonged to somebody else — permanently, on a deployment
// where nothing has been touched.
//
// This ran in production for the whole of this work. The console showed a
// customer that their audit chain was broken as its steady state, which is
// worse than showing nothing at all: the one signal that is supposed to mean
// tampering came to mean nothing.

func TestTenantViewDoesNotBreakTheChain(t *testing.T) {
	t.Setenv("PROMTACT_AUDIT_HMAC_SECRET", "test-audit-secret")
	store := New()

	// Records interleaved between tenants, which is the ordinary case on any
	// deployment with more than one customer.
	for i, tenant := range []string{"default", "acme", "default", "acme", "default"} {
		if err := store.AddAudit(domain.AuditEvent{
			ID:           fmt.Sprintf("au-%d", i),
			Timestamp:    time.Now().UTC(),
			Tenant:       tenant,
			Action:       "gateway.decide",
			ResourceType: "tool_call",
			Outcome:      "allow",
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	global := store.AuditChain()
	if !global.Valid {
		t.Fatalf("the fixture itself produced an invalid chain, so this test proves nothing")
	}

	for _, tenant := range []string{"default", "acme"} {
		view := store.AuditChainForTenant(tenant)
		if !view.Valid {
			t.Errorf("tenant %q was shown a broken chain: total=%d linked=%d unlinked=%d",
				tenant, view.Total, view.Linked, view.Unlinked)
		}
		if view.Head != global.Head {
			t.Errorf("tenant %q saw head %q, the chain's head is %q", tenant, view.Head, global.Head)
		}
		// The tenant still learns how much of the chain is theirs; that is a
		// count, and a count is all it can honestly be.
		if view.TenantRecords == 0 {
			t.Errorf("tenant %q was given no record count", tenant)
		}
		if view.TenantRecords >= view.Total {
			t.Errorf("tenant %q sees %d of %d records, expected strictly fewer",
				tenant, view.TenantRecords, view.Total)
		}
	}
}

// TestFilteringBreaksLinkageByConstruction shows why the tenant view cannot
// validate a subset, rather than asserting it as an opinion.
func TestFilteringBreaksLinkageByConstruction(t *testing.T) {
	t.Setenv("PROMTACT_AUDIT_HMAC_SECRET", "test-audit-secret")
	store := New()
	for i, tenant := range []string{"default", "acme", "default"} {
		if err := store.AddAudit(domain.AuditEvent{
			ID:           fmt.Sprintf("au-%d", i),
			Timestamp:    time.Now().UTC(),
			Tenant:       tenant,
			Action:       "gateway.decide",
			ResourceType: "tool_call",
			Outcome:      "allow",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Walk only one tenant's records and try to link them, exactly as the
	// removed code did.
	previous := ""
	broke := false
	for _, audit := range store.ListAuditsForTenant("default") {
		if audit.PrevHash != previous {
			broke = true
		}
		previous = audit.Hash
	}
	if !broke {
		t.Fatal("the fixture did not interleave tenants, so it cannot demonstrate the problem")
	}
	// And the same records, judged as the one chain they actually are.
	if !store.AuditChain().Valid {
		t.Error("the chain itself is invalid; the break is real rather than an artefact of filtering")
	}
}
