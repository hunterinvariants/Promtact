package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
)

func adminTestApp(t *testing.T) http.Handler {
	t.Helper()
	app, err := NewWithOptions(Options{Users: []auth.UserConfig{
		{Name: "platform", Tenant: "default", TokenHash: auth.HashToken("platform-token"), Roles: []string{auth.RoleAdmin}},
		{Name: "acme-admin", Tenant: "acme", TokenHash: auth.HashToken("acme-token"), Roles: []string{auth.RoleAdmin}},
		{Name: "watcher", Tenant: "default", TokenHash: auth.HashToken("viewer-token"), Roles: []string{auth.RoleViewer}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return app.Routes()
}

func adminRequest(handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// The generic read rule in RequiredRoles grants GETs to viewers. Provisioning
// paths must be exempt, or customer and key metadata would leak to any viewer.
func TestAdminPathsRequireAdminRole(t *testing.T) {
	for _, path := range []string{
		"/api/admin/tenants",
		"/api/admin/tenants/acme/users",
		"/api/admin/tenants/acme/keys",
		"/api/admin/tenants/acme/usage",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			roles := auth.RequiredRoles(method, path)
			if len(roles) != 1 || roles[0] != auth.RoleAdmin {
				t.Fatalf("%s %s should be admin-only, got %v", method, path, roles)
			}
		}
	}
}

func TestAdminTenantsRejectsViewer(t *testing.T) {
	handler := adminTestApp(t)
	if code := adminRequest(handler, http.MethodGet, "/api/admin/tenants", "viewer-token", "").Code; code != http.StatusForbidden {
		t.Fatalf("a viewer must not read the tenant list, got %d", code)
	}
	if code := adminRequest(handler, http.MethodPost, "/api/admin/tenants", "viewer-token", `{"tenant":"x"}`).Code; code != http.StatusForbidden {
		t.Fatalf("a viewer must not create tenants, got %d", code)
	}
}

// A customer's own admin is an admin inside their tenant, but must never be able
// to enumerate or create other customers.
func TestAdminTenantsRejectsTenantAdmin(t *testing.T) {
	handler := adminTestApp(t)
	if code := adminRequest(handler, http.MethodGet, "/api/admin/tenants", "acme-token", "").Code; code != http.StatusForbidden {
		t.Fatalf("a tenant admin must not read the platform tenant list, got %d", code)
	}
	if code := adminRequest(handler, http.MethodPost, "/api/admin/tenants", "acme-token", `{"tenant":"globex"}`).Code; code != http.StatusForbidden {
		t.Fatalf("a tenant admin must not create tenants, got %d", code)
	}
	if code := adminRequest(handler, http.MethodPost, "/api/admin/tenants/globex/status", "acme-token", `{"status":"suspended"}`).Code; code != http.StatusForbidden {
		t.Fatalf("a tenant admin must not suspend another tenant, got %d", code)
	}
}

func TestAdminTenantsRejectsAnonymous(t *testing.T) {
	handler := adminTestApp(t)
	if code := adminRequest(handler, http.MethodGet, "/api/admin/tenants", "", "").Code; code != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated request must be rejected, got %d", code)
	}
}

// The platform admin is authorized, but without a Postgres directory there is
// nothing to provision into: the API must say so cleanly instead of failing.
func TestAdminTenantsRequiresDirectory(t *testing.T) {
	handler := adminTestApp(t)
	rec := adminRequest(handler, http.MethodGet, "/api/admin/tenants", "platform-token", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without a postgres directory, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestNewAPIKeySecretIsUniqueAndPrefixed(t *testing.T) {
	seen := make(map[string]struct{}, 50)
	for i := 0; i < 50; i++ {
		secret, err := newAPIKeySecret()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(secret, "promtact_") || len(secret) != len("promtact_")+64 {
			t.Fatalf("unexpected key shape: %q", secret)
		}
		if _, dup := seen[secret]; dup {
			t.Fatal("generated a duplicate api key")
		}
		seen[secret] = struct{}{}
	}
}
