package store

import (
	"context"
	"reflect"
	"testing"
)

func TestEncodeDecodeRoles(t *testing.T) {
	encoded := encodeRoles([]string{" Operator ", "viewer", "operator", ""})
	if encoded != "operator,viewer" {
		t.Fatalf("roles should be normalized, deduped and sorted, got %q", encoded)
	}
	if got := decodeRoles("operator,viewer"); !reflect.DeepEqual(got, []string{"operator", "viewer"}) {
		t.Fatalf("decodeRoles mismatch: %v", got)
	}
	if got := decodeRoles(""); len(got) != 0 {
		t.Fatalf("empty roles should decode to empty, got %v", got)
	}
	if got := decodeRoles(" admin , , viewer "); !reflect.DeepEqual(got, []string{"admin", "viewer"}) {
		t.Fatalf("decodeRoles should trim and drop blanks, got %v", got)
	}
}

// Without a Postgres backend there is no tenant directory: the server falls back
// to policy.json users. Every directory call must degrade gracefully rather than
// panic, so self-hosted file/memory deployments keep working.
func TestDirectoryDegradesWithoutPostgres(t *testing.T) {
	s := New()
	ctx := context.Background()

	if s.HasDirectory() {
		t.Fatal("an in-memory store must not report a tenant directory")
	}
	if _, ok := s.IdentityByTokenHash(ctx, "somehash"); ok {
		t.Fatal("token lookup must not succeed without a directory")
	}
	if _, ok := s.IdentityByCredentials(ctx, "alice", "somehash"); ok {
		t.Fatal("credential lookup must not succeed without a directory")
	}
	if _, err := s.CreateTenantAccount(ctx, TenantAccount{Tenant: "acme"}); err == nil {
		t.Fatal("creating a tenant without a directory should error")
	}
	if _, err := s.ListTenantAccounts(ctx); err == nil {
		t.Fatal("listing tenants without a directory should error")
	}
	if err := s.RevokeAPIKey(ctx, "acme", "key-1"); err == nil {
		t.Fatal("revoking without a directory should error")
	}
}

func TestNewIDIsUniqueAndPrefixed(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := NewID("usr")
		if len(id) < 8 || id[:4] != "usr-" {
			t.Fatalf("unexpected id shape: %q", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id generated: %q", id)
		}
		seen[id] = struct{}{}
	}
}
