package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A stub directory that returns whatever identity the test hands it, so the
// rules can be exercised without a database.
type kindDirectory struct {
	identity Identity
}

func (d kindDirectory) IdentityByTokenHash(_ context.Context, _ string) (Identity, bool) {
	return d.identity, true
}

func (d kindDirectory) IdentityByCredentials(_ context.Context, _ string, _ string) (Identity, bool) {
	return d.identity, true
}

func withDirectory(t *testing.T, identity Identity) *Authenticator {
	t.Helper()
	t.Setenv("PROMTACT_SESSION_SECRET", "test-session-secret")
	authenticator := New(nil, "")
	authenticator.SetDirectory(kindDirectory{identity: identity})
	return authenticator
}

// A machine identity must never obtain a console session. It is the surface a
// second factor protects, and a service account has nobody behind it to present
// one — an exemption there would be permanent and invisible.
func TestServiceAccountCannotOpenASession(t *testing.T) {
	authenticator := withDirectory(t, Identity{
		Name: "agent-prod", Tenant: "acme", Roles: []string{RoleIngestor}, Kind: KindService,
	})

	if _, _, ok := authenticator.Login(context.Background(), "agent-prod", "some-key"); ok {
		t.Fatal("a service account was granted a session")
	}

	// Even handed a principal directly, minting must refuse: the rule cannot
	// depend on the directory query alone.
	if _, _, ok := authenticator.MintSession(Principal{
		Name: "agent-prod", Tenant: "acme", Roles: []string{RoleIngestor}, Kind: KindService,
	}); ok {
		t.Fatal("MintSession issued a session to a service account")
	}
}

// The same identity must still authenticate as a bearer token: agents are the
// reason service accounts exist, and blocking sessions must not block them.
func TestServiceAccountStillAuthenticatesWithItsKey(t *testing.T) {
	authenticator := withDirectory(t, Identity{
		Name: "agent-prod", Tenant: "acme", Roles: []string{RoleIngestor}, Kind: KindService,
	})

	request := httptest.NewRequest(http.MethodPost, "/api/gateway/decide", nil)
	request.Header.Set("Authorization", "Bearer some-key")

	principal, ok := authenticator.Authenticate(request)
	if !ok {
		t.Fatal("the service account could not authenticate with its key")
	}
	if !principal.IsService() {
		t.Fatalf("the principal lost its kind: %+v", principal)
	}
	if principal.Tenant != "acme" {
		t.Fatalf("wrong tenant: %+v", principal)
	}
}

func TestHumanAccountKeepsWorkingUnchanged(t *testing.T) {
	authenticator := withDirectory(t, Identity{
		Name: "alice", Tenant: "acme", Roles: []string{RoleOperator}, Kind: KindHuman,
	})

	info, session, ok := authenticator.Login(context.Background(), "alice", "her-key")
	if !ok {
		t.Fatal("a human account was refused a session")
	}
	if session == "" || info.Principal.Name != "alice" {
		t.Fatalf("unexpected session: %+v", info)
	}
	if info.Principal.IsService() {
		t.Fatal("a human was classified as a service account")
	}
}

// A record with a missing or unrecognised kind must be treated as a person.
// Defaulting the other way would hand any corrupted row the exemption that
// service accounts carry.
func TestUnknownKindDefaultsToHuman(t *testing.T) {
	for _, kind := range []string{"", "  ", "robot", "SERVICE_ACCOUNT"} {
		principal := principalFromIdentity(Identity{Name: "x", Tenant: "acme", Kind: kind})
		if principal.IsService() {
			t.Errorf("kind %q was treated as a service account", kind)
		}
		if principal.Kind != KindHuman {
			t.Errorf("kind %q normalised to %q, want %q", kind, principal.Kind, KindHuman)
		}
	}

	// The declared value must of course survive.
	if principalFromIdentity(Identity{Name: "x", Kind: "SERVICE"}).Kind != KindService {
		t.Error("an explicitly declared service account was not recognised")
	}
}
