package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
)

func breakglassApp(t *testing.T) *App {
	t.Helper()
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func openWindow(t *testing.T, app *App, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/breakglass", strings.NewReader(body))
	rec := httptest.NewRecorder()
	app.handleBreakglass(rec, req)
	return rec
}

// Announcing access is only worth something if the announcement says what it
// was for. A blank or throwaway reason produces a record that looks like
// accountability and carries none.
func TestBreakglassRequiresAMeaningfulReason(t *testing.T) {
	app := breakglassApp(t)

	for _, body := range []string{
		`{"reason":""}`,
		`{"reason":"   "}`,
		`{"reason":"fix"}`,
		`{"reason":"asdf"}`,
		`{}`,
	} {
		if rec := openWindow(t, app, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s was accepted with %d", body, rec.Code)
		}
	}

	rec := openWindow(t, app, `{"reason":"restoring the tenant that reported missing alerts","minutes":20}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a proper reason was refused: %d %s", rec.Code, rec.Body.String())
	}
}

// An unbounded window is indistinguishable from having no control, so the
// window is capped and a missing one gets a default rather than forever.
func TestBreakglassWindowIsBounded(t *testing.T) {
	app := breakglassApp(t)

	rec := openWindow(t, app, `{"reason":"investigating a failed migration","minutes":100000}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an unbounded window was accepted: %d", rec.Code)
	}

	rec = openWindow(t, app, `{"reason":"investigating a failed migration"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a window without minutes was refused: %d", rec.Code)
	}
	var session breakglassSession
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if !session.ExpiresAt.After(session.OpenedAt) {
		t.Fatal("the default window does not expire after it opens")
	}
	if session.ExpiresAt.Sub(session.OpenedAt) > breakglassMaxWindow {
		t.Fatal("the default window exceeds the cap")
	}
}

// The audit record is the durable half. It must exist before anything is
// notified, so a failed notification cannot lose the evidence.
func TestBreakglassIsAudited(t *testing.T) {
	app := breakglassApp(t)
	before := app.store.AuditChain().Total

	rec := openWindow(t, app, `{"reason":"restoring a tenant after the failed import","minutes":15}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("open failed: %d", rec.Code)
	}
	after := app.store.AuditChain()
	if after.Total <= before {
		t.Fatal("opening a break-glass window wrote no audit record")
	}
	if !after.Valid {
		t.Fatal("the audit chain stopped validating")
	}

	var session breakglassSession
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}

	closeReq := httptest.NewRequest(http.MethodPost, "/api/admin/breakglass/"+session.ID+"/close", nil)
	closeRec := httptest.NewRecorder()
	app.handleBreakglassClose(closeRec, closeReq)
	if closeRec.Code != http.StatusOK {
		t.Fatalf("close failed: %d %s", closeRec.Code, closeRec.Body.String())
	}
	if final := app.store.AuditChain(); final.Total <= after.Total {
		t.Fatal("closing a window wrote no audit record")
	}
}

// Covers is what the reconciler asks of every observed database session: was an
// announcement in force at that moment. Getting this wrong in either direction
// is bad — a false yes hides an unannounced access, a false no cries wolf.
func TestCoversAnswersTheReconcilersQuestion(t *testing.T) {
	register := newBreakglassRegister()
	now := time.Now().UTC()
	register.Open(breakglassSession{
		ID: "bg-1", Actor: "root", Reason: "a stated reason here",
		OpenedAt: now.Add(-10 * time.Minute), ExpiresAt: now.Add(10 * time.Minute),
	})

	if !register.Covers(now) {
		t.Error("a session inside the window was reported as uncovered")
	}
	if !register.Covers(now.Add(-10 * time.Minute)) {
		t.Error("the opening instant should be covered")
	}
	if !register.Covers(now.Add(10 * time.Minute)) {
		t.Error("the closing instant should be covered")
	}
	if register.Covers(now.Add(-11 * time.Minute)) {
		t.Error("access before the announcement was reported as covered")
	}
	if register.Covers(now.Add(11 * time.Minute)) {
		t.Error("access after the window was reported as covered")
	}

	// Once closed, the window no longer covers anything: an operator who closes
	// early must not keep the cover they gave back.
	if _, ok := register.Close("bg-1", now); !ok {
		t.Fatal("close failed")
	}
	if register.Covers(now) {
		t.Error("a closed window still covers access")
	}
}

// Announcing access is administration of the host, not a tenant operation.
func TestBreakglassIsAdminOnly(t *testing.T) {
	for _, path := range []string{"/api/admin/breakglass", "/api/admin/breakglass/bg-1/close"} {
		for _, method := range []string{"GET", "POST"} {
			required := auth.RequiredRoles(method, path)
			for _, role := range []string{auth.RoleViewer, auth.RoleIngestor, auth.RoleAnalyst, auth.RoleOperator} {
				if (auth.Principal{Name: "x", Roles: []string{role}}).HasAny(required...) {
					t.Errorf("%s %s is reachable for %s", method, path, role)
				}
			}
		}
	}
}
