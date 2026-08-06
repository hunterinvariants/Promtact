package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
)

func TestCanonicalAPIPath(t *testing.T) {
	cases := map[string]struct {
		want      string
		versioned bool
	}{
		"/api/v1/status":         {"/api/status", true},
		"/api/v1/gateway/decide": {"/api/gateway/decide", true},
		"/api/v1/admin/tenants":  {"/api/admin/tenants", true},
		"/api/status":            {"/api/status", false},
		"/api/v2/status":         {"/api/v2/status", false},
		"/api/v1":                {"/api/v1", false}, // no trailing slash: not a versioned path
		"/metrics":               {"/metrics", false},
	}
	for path, expect := range cases {
		got, versioned := canonicalAPIPath(path)
		if got != expect.want || versioned != expect.versioned {
			t.Errorf("canonicalAPIPath(%q) = (%q, %v), want (%q, %v)", path, got, versioned, expect.want, expect.versioned)
		}
	}
}

// The whole point of rewriting before authentication: authorization matches on
// concrete prefixes, so a versioned admin path must be just as admin-only as the
// original. Getting this wrong would hand customer data to any viewer.
func TestVersionedAdminPathKeepsAuthorization(t *testing.T) {
	app, err := NewWithOptions(Options{Users: []auth.UserConfig{
		{Name: "platform", Tenant: "default", TokenHash: auth.HashToken("platform-token"), Roles: []string{auth.RoleAdmin}},
		{Name: "watcher", Tenant: "default", TokenHash: auth.HashToken("viewer-token"), Roles: []string{auth.RoleViewer}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Routes()

	get := func(path, token string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	for _, path := range []string{"/api/admin/tenants", "/api/v1/admin/tenants"} {
		if code := get(path, "viewer-token"); code != http.StatusForbidden {
			t.Errorf("%s must stay admin-only, viewer got %d", path, code)
		}
		if code := get(path, ""); code != http.StatusUnauthorized {
			t.Errorf("%s must require authentication, anonymous got %d", path, code)
		}
	}
}

// A versioned path reaches the same handler as the original.
func TestVersionedPathReachesSameHandler(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Routes()

	body := `{"asset_id":"h","actor":"a","tool_name":"unlisted_destructive_tool","command":"wipe"}`
	for _, path := range []string{"/api/gateway/decide", "/api/v1/gateway/decide"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s returned %d: %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"verdict":"deny"`) {
			t.Fatalf("%s did not produce the expected verdict: %s", path, rec.Body.String())
		}
	}
}

func TestVersionHeaderAndDeprecationSignals(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Routes()

	versioned := httptest.NewRecorder()
	handler.ServeHTTP(versioned, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if got := versioned.Header().Get(apiVersionHeader); got != apiVersion {
		t.Fatalf("versioned request should report its version, got %q", got)
	}
	if versioned.Header().Get("Deprecation") != "" {
		t.Fatal("a versioned request must not be marked deprecated")
	}

	legacy := httptest.NewRecorder()
	handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if legacy.Header().Get("Deprecation") != "true" {
		t.Fatal("an unversioned API request should announce deprecation")
	}
	if link := legacy.Header().Get("Link"); !strings.Contains(link, "/api/v1/status") {
		t.Fatalf("the successor link should point at the versioned path, got %q", link)
	}

	// Non-API paths are not part of the versioned surface.
	console := httptest.NewRecorder()
	handler.ServeHTTP(console, httptest.NewRequest(http.MethodGet, "/", nil))
	if console.Header().Get("Deprecation") != "" {
		t.Fatal("the console must not be marked deprecated")
	}
}
