package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeDirectory struct {
	byToken       map[string]Identity
	byCredentials map[string]Identity
	tokenCalls    int
}

func (f *fakeDirectory) IdentityByTokenHash(_ context.Context, tokenHash string) (Identity, bool) {
	f.tokenCalls++
	identity, ok := f.byToken[tokenHash]
	return identity, ok
}

func (f *fakeDirectory) IdentityByCredentials(_ context.Context, username string, tokenHash string) (Identity, bool) {
	identity, ok := f.byCredentials[username+"|"+tokenHash]
	return identity, ok
}

func authRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestDirectoryPrincipalAuthenticates(t *testing.T) {
	directory := &fakeDirectory{byToken: map[string]Identity{
		HashToken("tenant-key"): {Name: "acme-bot", Tenant: "acme", Roles: []string{RoleOperator}},
	}}
	a := New(nil, "")
	a.SetDirectory(directory)

	principal, ok := a.Authenticate(authRequest("tenant-key"))
	if !ok {
		t.Fatal("a directory-provisioned key should authenticate")
	}
	if principal.Name != "acme-bot" || principal.Tenant != "acme" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	if !principal.HasAny(RoleOperator) {
		t.Fatalf("expected operator role, got %v", principal.Roles)
	}
	if _, ok := a.Authenticate(authRequest("wrong-key")); ok {
		t.Fatal("an unknown key must not authenticate")
	}
}

// Attaching a directory alone must enable authentication, otherwise a SaaS
// deployment with no policy.json users would serve traffic unauthenticated.
func TestDirectoryEnablesAuthentication(t *testing.T) {
	a := New(nil, "")
	if a.Enabled() {
		t.Fatal("an authenticator with no users and no directory is not enabled")
	}
	a.SetDirectory(&fakeDirectory{})
	if !a.Enabled() {
		t.Fatal("attaching a directory must enable authentication")
	}
}

// Configured users are the break-glass path: they must resolve without ever
// consulting the directory, so access survives a database outage.
func TestConfiguredUsersTakePrecedenceOverDirectory(t *testing.T) {
	directory := &fakeDirectory{byToken: map[string]Identity{
		HashToken("shared"): {Name: "from-directory", Tenant: "acme", Roles: []string{RoleViewer}},
	}}
	a := New([]UserConfig{{Name: "root", TokenHash: HashToken("shared"), Roles: []string{RoleAdmin}}}, "")
	a.SetDirectory(directory)

	principal, ok := a.Authenticate(authRequest("shared"))
	if !ok || principal.Name != "root" {
		t.Fatalf("the configured user should win, got %+v ok=%v", principal, ok)
	}
	if directory.tokenCalls != 0 {
		t.Fatal("the directory must not be consulted when a configured user matches")
	}
}

// Roles arriving from the directory pass through the same allowlist as
// configured users, so a tampered or stray role value cannot invent privileges.
func TestDirectoryRolesAreFiltered(t *testing.T) {
	directory := &fakeDirectory{byToken: map[string]Identity{
		HashToken("k"): {Name: "u", Tenant: "acme", Roles: []string{"superuser", "root", RoleViewer}},
	}}
	a := New(nil, "")
	a.SetDirectory(directory)

	principal, ok := a.Authenticate(authRequest("k"))
	if !ok {
		t.Fatal("expected authentication")
	}
	for _, role := range principal.Roles {
		if role != RoleViewer {
			t.Fatalf("unknown role %q survived normalization: %v", role, principal.Roles)
		}
	}
	if principal.HasAny(RoleAdmin) {
		t.Fatal("a made-up role must not grant admin")
	}
}

// A directory record with no usable roles falls back to the least privilege,
// never to an empty role set that might bypass a role check.
func TestDirectoryEmptyRolesFallBackToViewer(t *testing.T) {
	directory := &fakeDirectory{byToken: map[string]Identity{
		HashToken("k"): {Name: "u", Tenant: "", Roles: nil},
	}}
	a := New(nil, "")
	a.SetDirectory(directory)

	principal, ok := a.Authenticate(authRequest("k"))
	if !ok {
		t.Fatal("expected authentication")
	}
	if len(principal.Roles) != 1 || principal.Roles[0] != RoleViewer {
		t.Fatalf("expected viewer fallback, got %v", principal.Roles)
	}
	if principal.Tenant != "default" {
		t.Fatalf("an empty tenant should fall back to default, got %q", principal.Tenant)
	}
}

func TestDirectoryLoginByCredentials(t *testing.T) {
	directory := &fakeDirectory{byCredentials: map[string]Identity{
		"alice|" + HashToken("pw"): {Name: "alice", Tenant: "acme", Roles: []string{RoleAnalyst}},
	}}
	a := New(nil, "")
	a.SetDirectory(directory)

	if _, _, ok := a.Login(context.Background(), "alice", "pw"); !ok {
		t.Fatal("directory credentials should log in")
	}
	if _, _, ok := a.Login(context.Background(), "alice", "wrong"); ok {
		t.Fatal("a wrong key must not log in")
	}
	if _, _, ok := a.Login(context.Background(), "mallory", "pw"); ok {
		t.Fatal("a wrong user name must not log in")
	}
}
