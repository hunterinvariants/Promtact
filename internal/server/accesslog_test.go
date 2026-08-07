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
	req := httptest.NewRequest(http.MethodPost, "/api/access-log", strings.NewReader(body))
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
	if observed, unannounced := app.accessLogSummary(); observed != 1 || unannounced != 0 {
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
	if observed, _ := app.accessLogSummary(); observed != 0 {
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

	if app.accessLog.heartbeat().IsZero() {
		t.Fatal("a heartbeat did not register")
	}
	if observed, _ := app.accessLogSummary(); observed != 0 {
		t.Fatalf("a heartbeat counted as a session: %d", observed)
	}
}

// The shipper runs unattended on the host. Its credential must let it report
// observations and nothing else — putting the deployment's most privileged
// token in its least protected place is exactly the escalation this product
// exists to prevent elsewhere.
func TestReporterCanReportAndNothingElse(t *testing.T) {
	reporter := auth.Principal{Name: "access-shipper", Roles: []string{auth.RoleReporter}}

	if !reporter.HasAny(auth.RequiredRoles(http.MethodPost, "/api/access-log")...) {
		t.Fatal("the reporter cannot submit observations, which is its only job")
	}

	// Everything a compromised shipper must not be able to reach.
	forbidden := []struct{ method, path string }{
		{"POST", "/api/admin/tenants"},
		{"GET", "/api/admin/tenants"},
		{"POST", "/api/admin/breakglass"},
		{"GET", "/api/admin/access-log"},
		{"GET", "/api/audit/witness"},
		{"GET", "/metrics"},
		{"POST", "/api/scim/v2/Users"},
		{"POST", "/api/gateway/decide"},
		{"POST", "/api/events"},
		{"GET", "/api/alerts"},
		{"GET", "/api/audit"},
		{"POST", "/api/policy/reload"},
	}
	for _, target := range forbidden {
		if reporter.HasAny(auth.RequiredRoles(target.method, target.path)...) {
			t.Errorf("a reporter can reach %s %s", target.method, target.path)
		}
	}
}

// Reading the findings is analyst work; submitting them is not.
func TestReadingFindingsIsSeparateFromSubmittingThem(t *testing.T) {
	analyst := auth.Principal{Name: "sam", Roles: []string{auth.RoleAnalyst}}
	if !analyst.HasAny(auth.RequiredRoles(http.MethodGet, "/api/access-log")...) {
		t.Error("an analyst cannot read the access-log findings")
	}
	if analyst.HasAny(auth.RequiredRoles(http.MethodPost, "/api/access-log")...) {
		t.Error("an analyst can submit observations, which only the shipper should do")
	}

	// A viewer has no business here in either direction.
	viewer := auth.Principal{Name: "vic", Roles: []string{auth.RoleViewer}}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if viewer.HasAny(auth.RequiredRoles(method, "/api/access-log")...) {
			t.Errorf("a viewer can %s the access log", method)
		}
	}
}

// The endpoint must not be served from under /api/admin/ any more: a path there
// demands admin, which is the escalation this change removes.
func TestAccessLogIsNotUnderTheAdminPrefix(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/access-log", nil))
	if rec.Code == http.StatusOK {
		t.Fatal("the access log is still served under the admin prefix")
	}
}

// The summary an auditor reads must not be resettable by whoever can restart the
// service — which is exactly the operator this control watches. It is derived
// from the durable audit records, so wiping the process state changes nothing.
func TestFindingsSurviveLosingProcessState(t *testing.T) {
	app := breakglassApp(t)

	now := time.Now().UTC().Format(time.RFC3339)
	submitSessions(t, app, fmt.Sprintf(
		`{"sessions":[{"at":%q,"user":"promtact","application":"psql","event":"connect"}]}`, now))

	observed, unannounced := app.accessLogSummary()
	if observed != 1 || unannounced != 1 {
		t.Fatalf("the finding was not recorded: observed=%d unannounced=%d", observed, unannounced)
	}

	// Everything the process holds in memory is thrown away, as a restart would.
	app.accessLog = &accessLogState{}
	app.breakglass = newBreakglassRegister()

	observed, unannounced = app.accessLogSummary()
	if observed != 1 || unannounced != 1 {
		t.Fatalf("the finding was lost with the process state: observed=%d unannounced=%d", observed, unannounced)
	}
}
