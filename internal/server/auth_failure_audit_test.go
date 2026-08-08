package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
)

// An anonymous caller must not be able to append to the hash chain on demand.
//
// On the live deployment a single browser tab with an expired session polled
// three endpoints every ten seconds and produced 2432 audit records - more than
// two thirds of the entire chain. The trail that answers "what did the agent do
// and who let it" was mostly one logged-out frontend, and the noise arrived
// faster than the decisions did.
//
// It is also a way in: anyone able to reach the endpoint could grow the chain
// without credentials, filling the database and pushing real decisions past the
// retention window.

func rateLimitApp(t *testing.T) *App {
	t.Helper()
	app, err := NewWithOptions(Options{
		Users: []auth.UserConfig{{
			Name:      "operator",
			TokenHash: auth.HashToken("secret"),
			Roles:     []string{auth.RoleAdmin},
		}},
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app
}

func auditRecordCount(t *testing.T, app *App) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reading the audit trail: %d", rec.Code)
	}
	var records []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &records); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	return len(records)
}

func TestRepeatedAuthFailuresDoNotFillTheChain(t *testing.T) {
	app := rateLimitApp(t)
	before := auditRecordCount(t, app)

	// The exact production pattern: one source, one path, many rejections.
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/audit/chain", nil)
		req.RemoteAddr = "203.0.113.7:5000"
		rec := httptest.NewRecorder()
		app.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("call %d should have been rejected, got %d", i, rec.Code)
		}
	}

	added := auditRecordCount(t, app) - before
	if added != 1 {
		t.Fatalf("50 rejected calls produced %d audit records, want 1 - an anonymous "+
			"caller can still grow the chain at will", added)
	}
}

// Suppressing must not mean losing. The first rejection is recorded, and the
// next one carries how many were suppressed - one probe is noise, four hundred
// in five minutes is a finding, and only the count tells them apart.
func TestTheSuppressedCountIsRecorded(t *testing.T) {
	app := rateLimitApp(t)

	// First window: one recorded, several suppressed.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/audit/chain", nil)
		req.RemoteAddr = "203.0.113.8:5000"
		app.Routes().ServeHTTP(httptest.NewRecorder(), req)
	}
	// Force the window open, as the passage of time would.
	app.authFailureMu.Lock()
	for _, state := range app.authFailures {
		state.firstSeen = state.firstSeen.Add(-2 * authFailureWindow)
	}
	app.authFailureMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/audit/chain", nil)
	req.RemoteAddr = "203.0.113.8:5000"
	app.Routes().ServeHTTP(httptest.NewRecorder(), req)

	read := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	read.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, read)
	if !strings.Contains(rec.Body.String(), "suppressed_since_last") {
		t.Fatalf("the suppressed count was not recorded, so a flood looks like a single "+
			"probe: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"suppressed_since_last":"4"`) {
		t.Errorf("expected 4 suppressed rejections in the record; body was %s", rec.Body.String())
	}
}

// Different sources must be tracked separately, or one noisy client would hide
// a real one behind it.
func TestDifferentSourcesAreRecordedSeparately(t *testing.T) {
	app := rateLimitApp(t)
	before := auditRecordCount(t, app)

	for _, address := range []string{"203.0.113.10:1", "203.0.113.11:1", "203.0.113.12:1"} {
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest(http.MethodGet, "/api/audit/chain", nil)
			req.RemoteAddr = address
			app.Routes().ServeHTTP(httptest.NewRecorder(), req)
		}
	}

	if added := auditRecordCount(t, app) - before; added != 3 {
		t.Fatalf("three sources produced %d records, want one each", added)
	}
}

// And different paths from the same source, because "probing /api/audit" and
// "probing every endpoint in turn" are different events.
func TestDifferentPathsAreRecordedSeparately(t *testing.T) {
	app := rateLimitApp(t)
	before := auditRecordCount(t, app)

	for _, path := range []string{"/api/audit/chain", "/api/audit/witness", "/api/responses"} {
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = "203.0.113.20:1"
			app.Routes().ServeHTTP(httptest.NewRecorder(), req)
		}
	}

	if added := auditRecordCount(t, app) - before; added != 3 {
		t.Fatalf("three paths produced %d records, want one each", added)
	}
}
