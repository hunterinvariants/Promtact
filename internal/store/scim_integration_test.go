package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Provisioning is where multi-tenant isolation is easiest to lose: an id is
// opaque, so a lookup that resolves it first and checks ownership afterwards
// leaks one customer's directory to another's identity provider. The tenant is
// part of the query, and this proves it.
func TestDirectoryLookupsAreTenantScoped(t *testing.T) {
	dsn := os.Getenv("PROMTACT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PROMTACT_TEST_POSTGRES_DSN to run Postgres integration tests")
	}
	s, err := NewWithPostgres(dsn)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	if s.SchemaVersion() < 7 {
		t.Fatalf("expected schema version >= 7, got %d", s.SchemaVersion())
	}

	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	alpha, beta := "scim-a-"+suffix, "scim-b-"+suffix
	for _, tenant := range []string{alpha, beta} {
		if _, err := s.CreateTenantAccount(ctx, TenantAccount{Tenant: tenant}); err != nil {
			t.Fatalf("create tenant %s: %v", tenant, err)
		}
	}

	victim, err := s.CreateTenantUser(ctx, TenantUser{
		Tenant: alpha, Name: "victim-" + suffix, Roles: []string{"operator"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Reading another tenant's user must be indistinguishable from the user not
	// existing — reporting "forbidden" would confirm the id is real.
	if _, found, err := s.UserByID(ctx, beta, victim.ID); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("a user resolved through a foreign tenant")
	}
	if _, found, err := s.UserByID(ctx, alpha, victim.ID); err != nil || !found {
		t.Fatalf("the owning tenant could not read its own user (found=%v err=%v)", found, err)
	}

	// Writes must be scoped the same way, or a foreign identity provider could
	// grant itself admin on someone else's account.
	if err := s.SetUserRoles(ctx, beta, victim.ID, []string{"admin"}); err == nil {
		t.Fatal("a foreign tenant changed a user's roles")
	}
	after, _, err := s.UserByID(ctx, alpha, victim.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(after.Roles, ",") != "operator" {
		t.Fatalf("roles were modified across tenants: %v", after.Roles)
	}

	// Listing must return only the caller's own tenant.
	others, err := s.ListTenantUsers(ctx, beta)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range others {
		if user.ID == victim.ID {
			t.Fatal("a foreign tenant's user appeared in the listing")
		}
	}

	// And the owning tenant's own write still works.
	if err := s.SetUserRoles(ctx, alpha, victim.ID, []string{"analyst"}); err != nil {
		t.Fatalf("the owning tenant could not update its own user: %v", err)
	}
}
