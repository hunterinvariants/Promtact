package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
)

// Provisioning creates principals and revokes their access, so it is admin-only
// for every method. Left to the generic read rule, any viewer could enumerate
// the customer's entire user directory over GET.
func TestSCIMRequiresAdminForEveryMethod(t *testing.T) {
	paths := []string{
		"/api/scim/v2/Users",
		"/api/scim/v2/Users/usr-123",
		"/api/scim/v2/ServiceProviderConfig",
	}
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

	for _, path := range paths {
		for _, method := range methods {
			required := auth.RequiredRoles(method, path)
			for _, role := range []string{auth.RoleViewer, auth.RoleIngestor, auth.RoleAnalyst, auth.RoleOperator} {
				principal := auth.Principal{Name: "someone", Roles: []string{role}}
				if principal.HasAny(required...) {
					t.Errorf("%s %s is reachable for %s", method, path, role)
				}
			}
			admin := auth.Principal{Name: "root", Roles: []string{auth.RoleAdmin}}
			if !admin.HasAny(required...) {
				t.Errorf("%s %s is unreachable for admin", method, path)
			}
		}
	}
}

// The authentication middleware only guards /api/ and /metrics. A SCIM mount at
// the conventional /scim/v2 root would therefore be served with no
// authentication at all — a provisioning API open to the internet. This test
// exists to fail if anyone ever adds one.
func TestNoProvisioningEndpointOutsideTheGuardedPrefix(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Routes()

	// These paths do answer — the console serves its single-page app for
	// anything it does not recognise — so the meaningful assertion is that no
	// SCIM handler is behind them. A SCIM content type here would mean
	// provisioning is answering outside the authenticated prefix.
	for _, path := range []string{"/scim/v2/Users", "/scim/v2/ServiceProviderConfig", "/scim/v2/Users/usr-1"} {
		for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			if strings.Contains(rec.Header().Get("Content-Type"), "scim") {
				t.Errorf("%s %s is answered by SCIM outside the authenticated prefix", method, path)
			}
			if strings.Contains(rec.Body.String(), "urn:ietf:params:scim") {
				t.Errorf("%s %s returned a SCIM payload outside the authenticated prefix", method, path)
			}
		}
	}
}

// Roles arrive from an identity provider and are attacker-influenceable through
// group membership. An unmapped value must grant nothing rather than being
// stored verbatim and later matched against.
func TestSCIMRolesPassThroughTheAllowlist(t *testing.T) {
	got := scimRoles([]string{"operator", "superuser", "admin ", "ANALYST", "'; DROP TABLE", ""})
	want := map[string]bool{"operator": true, "admin": true, "analyst": true}
	if len(got) != len(want) {
		t.Fatalf("unexpected roles: %v", got)
	}
	for _, role := range got {
		if !want[role] {
			t.Errorf("%q survived the allowlist", role)
		}
	}

	// An account with no recognisable role must not end up with none at all,
	// which would be an unusable principal, nor with a privileged default.
	fallback := scimRoles([]string{"domain-admins", "everyone"})
	if len(fallback) != 1 || fallback[0] != auth.RoleViewer {
		t.Fatalf("unmapped roles should fall back to viewer, got %v", fallback)
	}
}

// Only one filter form is supported. A general filter language would put a
// parser accepting attacker-supplied input in front of the directory for no
// provisioning benefit, so everything else must be refused rather than ignored.
func TestSCIMFilterAcceptsOnlyUserNameEquality(t *testing.T) {
	value, ok := parseSCIMUserNameFilter(`userName eq "alice@example.com"`)
	if !ok || value != "alice@example.com" {
		t.Fatalf("the supported filter was rejected: %q %v", value, ok)
	}
	if value, ok := parseSCIMUserNameFilter(`USERNAME EQ "bob"`); !ok || value != "bob" {
		t.Errorf("filters are case sensitive: %q %v", value, ok)
	}

	for _, bad := range []string{
		"",
		"userName",
		`userName co "ali"`,
		`active eq true`,
		`userName eq alice`,
		`userName eq "a" and roles eq "admin"`,
	} {
		if _, ok := parseSCIMUserNameFilter(bad); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// A credential that is not bound to a tenant must not be able to provision.
// Without this the tenant would fall back to a default and an unscoped token
// would write into a real customer's directory.
func TestSCIMRefusesATenantlessCredential(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if _, ok := app.scimTenant(rec, auth.Principal{Name: "root", Roles: []string{auth.RoleAdmin}}); ok {
		t.Fatal("a principal without a tenant was allowed to provision")
	}
	if rec.Code == http.StatusOK {
		t.Fatalf("expected a refusal, got %d", rec.Code)
	}
}

// Errors must use the SCIM schema: an IdP parses these to decide whether to
// retry or to raise the problem with an administrator, and this service's
// generic error shape would make failures opaque at the far end.
func TestSCIMErrorsUseTheSCIMSchema(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSCIMError(rec, http.StatusConflict, "uniqueness", "already exists")

	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, scimContentType) {
		t.Errorf("wrong content type: %q", contentType)
	}
	var body struct {
		Schemas  []string `json:"schemas"`
		Status   string   `json:"status"`
		SCIMType string   `json:"scimType"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Schemas) != 1 || body.Schemas[0] != scimErrorSchema {
		t.Errorf("wrong schema: %v", body.Schemas)
	}
	// RFC 7644 carries status as a string, and IdPs that expect one break on a
	// number.
	if body.Status != "409" || body.SCIMType != "uniqueness" {
		t.Errorf("unexpected body: %+v", body)
	}
}
