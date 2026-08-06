package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/domain"
	"github.com/hunterinvariants/promtact/internal/store"
)

// degradedApp builds a server whose storage then breaks, standing in for the
// database going away underneath a running deployment.
//
// The app is constructed while the data path is still usable — reading a
// not-yet-existing snapshot succeeds on every platform — and only afterwards is
// the parent directory replaced by a regular file, so every subsequent write
// fails. Breaking the path up front is not portable: reading through a file
// yields ENOTDIR on Linux but a plain "not found" on Windows, so construction
// would fail on one platform and succeed on the other.
func degradedApp(t *testing.T) (*App, string) {
	t.Helper()
	root := t.TempDir()
	parent := filepath.Join(root, "storage")
	journalPath := filepath.Join(root, "decisions.jsonl")

	app, err := NewWithOptions(Options{
		DataPath:            filepath.Join(parent, "snapshot.json"),
		DecisionJournalPath: journalPath,
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	// Storage goes away: the directory the snapshot lives in is now a file, so
	// creating or writing it fails the way an unreachable database would.
	if err := os.RemoveAll(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte("storage is gone"), 0o600); err != nil {
		t.Fatal(err)
	}
	return app, journalPath
}

func decide(t *testing.T, app *App, body string) (*httptest.ResponseRecorder, domain.ToolCallDecision) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/decide", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	var decision domain.ToolCallDecision
	if rec.Code == http.StatusOK || rec.Code == http.StatusAccepted {
		if err := json.Unmarshal(rec.Body.Bytes(), &decision); err != nil {
			t.Fatalf("decode decision: %v (body %s)", err, rec.Body.String())
		}
	}
	return rec, decision
}

// The core claim: a storage outage must not discard an enforcement verdict.
// A denial answered with 500 is dangerous, because a caller that treats an error
// as "gateway unavailable" would proceed with the call that was just blocked.
func TestDenyIsStillEnforcedWhenStorageFails(t *testing.T) {
	app, _ := degradedApp(t)

	rec, decision := decide(t, app, `{"asset_id":"h","actor":"a","tool_name":"unlisted_destructive_tool","command":"wipe"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("a decision must still be served during an outage, got %d: %s", rec.Code, rec.Body.String())
	}
	if decision.Verdict != domain.GatewayDeny {
		t.Fatalf("the verdict must be unchanged by the outage, got %s (%s)", decision.Verdict, decision.Reason)
	}
	if decision.Reason == "" {
		t.Fatal("the served decision must still explain itself")
	}

	degraded, since, reason := app.DegradedState()
	if !degraded {
		t.Fatal("the deployment must report degraded persistence")
	}
	if since.IsZero() || reason == "" {
		t.Fatalf("degraded state must be attributable: since=%v reason=%q", since, reason)
	}
	if app.journal.Depth() == 0 {
		t.Fatal("the record must be journalled so it is not lost")
	}
}

// Approval-required verdicts stay approval-required: the caller is told to wait,
// and because the pending action cannot be recorded it also cannot be approved
// during the outage, which is the safe direction.
func TestApprovalVerdictSurvivesStorageFailure(t *testing.T) {
	app, _ := degradedApp(t)

	rec, decision := decide(t, app, `{"asset_id":"h","actor":"a","tool_name":"asset_inventory","command":"read the api_key and ssh_key material"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected a served decision, got %d: %s", rec.Code, rec.Body.String())
	}
	if decision.Verdict != domain.GatewayRequireApproval {
		t.Fatalf("expected require_approval, got %s (%s)", decision.Verdict, decision.Reason)
	}
	if len(decision.Alerts) == 0 {
		t.Fatal("alerts must still be reported to the caller during an outage")
	}
}

// Nothing is lost: once storage recovers the journalled records are replayed and
// the journal empties.
func TestJournalledDecisionsReconcileAfterRecovery(t *testing.T) {
	app, _ := degradedApp(t)

	for i := 0; i < 3; i++ {
		if rec, _ := decide(t, app, `{"asset_id":"h","actor":"a","tool_name":"unlisted_destructive_tool","command":"wipe"}`); rec.Code != http.StatusAccepted {
			t.Fatalf("decision %d was not served: %d", i, rec.Code)
		}
	}
	backlog := app.journal.Depth()
	if backlog == 0 {
		t.Fatal("expected a journal backlog while storage is down")
	}

	// Storage recovers.
	app.store = store.New()

	applied, err := app.ReconcileJournal()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if applied != backlog {
		t.Fatalf("expected all %d journalled records to replay, got %d", backlog, applied)
	}
	if app.journal.Depth() != 0 {
		t.Fatalf("journal should be empty after reconciliation, got %d", app.journal.Depth())
	}

	_, alertCount, _, actionCount, _ := app.store.Counts()
	if alertCount == 0 && actionCount == 0 {
		t.Fatal("reconciliation did not restore any records into storage")
	}
}

// Healthy storage must behave exactly as before: nothing journalled, not
// degraded. The degraded path must not become the normal path.
func TestHealthyStorageDoesNotJournal(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "decisions.jsonl")
	app, err := NewWithOptions(Options{DecisionJournalPath: journalPath})
	if err != nil {
		t.Fatal(err)
	}

	rec, decision := decide(t, app, `{"asset_id":"h","actor":"a","tool_name":"unlisted_destructive_tool","command":"wipe"}`)
	if rec.Code != http.StatusAccepted || decision.Verdict != domain.GatewayDeny {
		t.Fatalf("unexpected healthy-path result: %d %s", rec.Code, decision.Verdict)
	}
	if degraded, _, _ := app.DegradedState(); degraded {
		t.Fatal("healthy storage must not report degraded mode")
	}
	if app.journal.Depth() != 0 {
		t.Fatalf("healthy storage must not journal, got depth %d", app.journal.Depth())
	}
}
