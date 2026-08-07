package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
)

func submitSessions(t *testing.T, app *App, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/access-log", strings.NewReader(body))
	rec := httptest.NewRecorder()
	app.handleAccessLog(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submission failed: %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// The whole point: a database session nobody announced becomes a finding.
func TestUnannouncedSessionIsFlaggedAndAudited(t *testing.T) {
	app := breakglassApp(t)
	before := app.store.AuditChain().Total

	now := time.Now().UTC().Format(time.RFC3339)
	result := submitSessions(t, app, fmt.Sprintf(
		`{"sessions":[{"at":%q,"user":"promtact","application":"psql","source":"127.0.0.1","database":"promtact","event":"connect"}]}`, now))

	if result["unannounced"].(float64) != 1 {
		t.Fatalf("an unannounced session was not flagged: %v", result)
	}
	after := app.store.AuditChain()
	if after.Total <= before {
		t.Fatal("no audit record was written for the unannounced session")
	}
	if !after.Valid {
		t.Fatal("the audit chain stopped validating")
	}
}

// A session inside an announced window is ordinary. It is still recorded — the
// record is the point — but it must not raise an alarm, or the alarm becomes
// noise and gets ignored.
func TestAnnouncedSessionIsNotFlagged(t *testing.T) {
	app := breakglassApp(t)

	rec := openWindow(t, app, `{"reason":"restoring the tenant that lost its keys","minutes":30}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("could not announce: %d", rec.Code)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result := submitSessions(t, app, fmt.Sprintf(
		`{"sessions":[{"at":%q,"user":"promtact","application":"psql","source":"127.0.0.1","event":"connect"}]}`, now))

	if result["unannounced"].(float64) != 0 {
		t.Fatalf("an announced session was flagged: %v", result)
	}
	if _, observed, unannounced := app.accessLog.snapshot(); observed != 1 || unannounced != 0 {
		t.Fatalf("counters wrong: observed=%d unannounced=%d", observed, unannounced)
	}
}

// The service's own connections are the expected traffic. Counting them would
// bury the one session that matters under thousands that do not.
func TestTheServicesOwnSessionsAreIgnored(t *testing.T) {
	app := breakglassApp(t)

	now := time.Now().UTC().Format(time.RFC3339)
	result := submitSessions(t, app, fmt.Sprintf(
		`{"sessions":[
			{"at":%q,"user":"promtact","application":"promtact","event":"connect"},
			{"at":%q,"user":"promtact","application":"PROMTACT","event":"connect"}
		]}`, now, now))

	if result["unannounced"].(float64) != 0 {
		t.Fatalf("the service's own sessions were flagged: %v", result)
	}
	if _, observed, _ := app.accessLog.snapshot(); observed != 0 {
		t.Fatalf("the service's own sessions were counted: %d", observed)
	}
}

// A reconciler that reacts only to what arrives is defeated by arranging for
// nothing to arrive. Silence after the shipper has been heard from is itself a
// signal — but silence before it was ever heard from is not, or an unconfigured
// deployment would alarm forever and train the operator to ignore it.
func TestShipperSilenceIsASignalOnlyOnceItHasReported(t *testing.T) {
	app := breakglassApp(t)

	if app.accessLogSilent() {
		t.Fatal("a deployment that never configured the shipper reports it as silent")
	}

	app.accessLog.noteHeartbeat(time.Now().UTC())
	if app.accessLogSilent() {
		t.Fatal("a shipper that just reported was called silent")
	}

	app.accessLog.noteHeartbeat(time.Now().UTC().Add(-accessLogSilenceAfter - time.Minute))
	if !app.accessLogSilent() {
		t.Fatal("a shipper that stopped reporting was not noticed")
	}
}

// A heartbeat with no sessions still proves the shipper is alive.
func TestHeartbeatWithoutSessionsCounts(t *testing.T) {
	app := breakglassApp(t)
	submitSessions(t, app, `{"heartbeat":true,"sessions":[]}`)

	last, observed, _ := app.accessLog.snapshot()
	if last.IsZero() {
		t.Fatal("a heartbeat did not register")
	}
	if observed != 0 {
		t.Fatalf("a heartbeat counted as a session: %d", observed)
	}
}

func TestAccessLogIsAdminOnly(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		required := auth.RequiredRoles(method, "/api/admin/access-log")
		for _, role := range []string{auth.RoleViewer, auth.RoleIngestor, auth.RoleAnalyst, auth.RoleOperator} {
			if (auth.Principal{Name: "x", Roles: []string{role}}).HasAny(required...) {
				t.Errorf("%s /api/admin/access-log is reachable for %s", method, role)
			}
		}
	}
}
