package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"
)

func hashForTest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// TestTenantDirectoryIntegration exercises the SaaS tenant directory against a
// real Postgres: schema migration, provisioning, and the security properties
// that gate authentication (revocation, tenant suspension, name uniqueness).
func TestTenantDirectoryIntegration(t *testing.T) {
	dsn := os.Getenv("PROMTACT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PROMTACT_TEST_POSTGRES_DSN to run Postgres integration tests")
	}

	s, err := NewWithPostgres(dsn)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	if s.SchemaVersion() < 5 {
		t.Fatalf("expected schema version >= 5 (tenant directory), got %d", s.SchemaVersion())
	}
	if !s.HasDirectory() {
		t.Fatal("a postgres store must report a tenant directory")
	}

	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	tenantA := "it-tenant-a-" + suffix
	tenantB := "it-tenant-b-" + suffix
	userName := "it-user-" + suffix
	token := "it-token-" + suffix
	tokenHash := hashForTest(token)

	defer func() {
		db, err := s.directoryDB()
		if err != nil {
			return
		}
		// api keys and users cascade from the tenant rows.
		_, _ = db.ExecContext(ctx, `DELETE FROM promtact_tenant_accounts WHERE tenant IN ($1, $2)`, tenantA, tenantB)
	}()

	if _, err := s.CreateTenantAccount(ctx, TenantAccount{Tenant: tenantA, DisplayName: "Acme"}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if _, err := s.CreateTenantAccount(ctx, TenantAccount{Tenant: tenantB, DisplayName: "Globex"}); err != nil {
		t.Fatalf("create second tenant: %v", err)
	}

	user, err := s.CreateTenantUser(ctx, TenantUser{Tenant: tenantA, Name: userName, Roles: []string{"operator"}})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// A user name must be globally unique, otherwise a login name would resolve
	// ambiguously across tenants.
	if _, err := s.CreateTenantUser(ctx, TenantUser{Tenant: tenantB, Name: strings.ToUpper(userName), Roles: []string{"viewer"}}); err == nil {
		t.Fatal("a duplicate user name across tenants must be rejected")
	}

	key, err := s.CreateAPIKey(ctx, APIKey{Tenant: tenantA, UserID: user.ID, Name: "primary"}, tokenHash)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	identity, ok, _ := s.IdentityByTokenHash(ctx, tokenHash)
	if !ok {
		t.Fatal("a valid key should resolve an identity")
	}
	if identity.Tenant != tenantA || identity.Name != userName {
		t.Fatalf("identity mismatch: %+v", identity)
	}
	if len(identity.Roles) != 1 || identity.Roles[0] != "operator" {
		t.Fatalf("roles mismatch: %v", identity.Roles)
	}

	if _, ok, _ := s.IdentityByCredentials(ctx, userName, tokenHash); !ok {
		t.Fatal("credential login should succeed for the owning user")
	}
	if _, ok, _ := s.IdentityByCredentials(ctx, "someone-else", tokenHash); ok {
		t.Fatal("a key must not authenticate a different user name")
	}

	// Suspending the tenant must immediately stop authentication.
	if err := s.SetTenantAccountStatus(ctx, tenantA, StatusSuspended); err != nil {
		t.Fatalf("suspend tenant: %v", err)
	}
	if _, ok, _ := s.IdentityByTokenHash(ctx, tokenHash); ok {
		t.Fatal("a suspended tenant must not authenticate")
	}
	if err := s.SetTenantAccountStatus(ctx, tenantA, StatusActive); err != nil {
		t.Fatalf("reactivate tenant: %v", err)
	}
	if _, ok, _ := s.IdentityByTokenHash(ctx, tokenHash); !ok {
		t.Fatal("reactivating the tenant should restore authentication")
	}

	// Revoking the key must immediately stop authentication.
	if err := s.RevokeAPIKey(ctx, tenantA, key.ID); err != nil {
		t.Fatalf("revoke key: %v", err)
	}
	if _, ok, _ := s.IdentityByTokenHash(ctx, tokenHash); ok {
		t.Fatal("a revoked key must not authenticate")
	}
	if err := s.RevokeAPIKey(ctx, tenantA, key.ID); err == nil {
		t.Fatal("revoking an already revoked key should error")
	}

	keys, err := s.ListAPIKeys(ctx, tenantA)
	if err != nil || len(keys) != 1 || keys[0].RevokedAt == nil {
		t.Fatalf("expected one revoked key, got %+v (err=%v)", keys, err)
	}
	users, err := s.ListTenantUsers(ctx, tenantA)
	if err != nil || len(users) != 1 || users[0].Name != userName {
		t.Fatalf("expected the created user, got %+v (err=%v)", users, err)
	}
}
