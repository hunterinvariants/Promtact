package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/policy"
)

// Declining a held call, and the rules that make it an answer rather than a
// pause.

func heldAction(t *testing.T, app *App) string {
	t.Helper()
	code, body := runDemo(t, app)
	if code != http.StatusOK {
		t.Fatalf("the demonstration did not run: %d %s", code, body)
	}
	// The guarded run ends with an outward call held for a person; that is the
	// action under test.
	req := httptest.NewRequest(http.MethodGet, "/api/gateway/queue", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	var queue struct {
		PendingActions []struct {
			ID string `json:"id"`
		} `json:"pending_actions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &queue); err != nil {
		t.Fatalf("decoding the queue: %v (%s)", err, rec.Body.String())
	}
	if len(queue.PendingActions) == 0 {
		t.Fatal("the guarded run left nothing in the approval queue, so there is nothing to decline")
	}
	return queue.PendingActions[0].ID
}

func post(t *testing.T, app *App, path string, payload any) (int, string) {
	t.Helper()
	encoded, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestDecliningRemovesTheCallFromTheQueueWithoutExecutingIt(t *testing.T) {
	app := demoApp(t, []policy.AgentIdentity{
		{AgentID: "other", KeyHash: policy.HashAgentToken("unrelated")},
	})
	id := heldAction(t, app)

	outboxBefore := app.demoOutboxCount()

	code, body := post(t, app, "/api/responses/decline", map[string]string{
		"action_id": id, "reason": "not something this agent should be doing",
	})
	if code != http.StatusOK {
		t.Fatalf("declining failed: %d %s", code, body)
	}

	// The point of declining rather than approving: nothing happens.
	if after := app.demoOutboxCount(); after != outboxBefore {
		t.Fatalf("declining executed the action anyway: outbox went from %d to %d", outboxBefore, after)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/gateway/queue", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), id) {
		t.Fatalf("the declined action is still in the queue: %s", rec.Body.String())
	}
}

// A decline is terminal. If it could be overturned by a later approval, the
// record could not say which answer the person actually gave.
func TestADeclinedActionCannotThenBeApproved(t *testing.T) {
	app := demoApp(t, []policy.AgentIdentity{
		{AgentID: "other", KeyHash: policy.HashAgentToken("unrelated")},
	})
	id := heldAction(t, app)

	if code, body := post(t, app, "/api/responses/decline", map[string]string{
		"action_id": id, "reason": "no",
	}); code != http.StatusOK {
		t.Fatalf("declining failed: %d %s", code, body)
	}

	code, body := post(t, app, "/api/responses/approve", map[string]string{
		"action_id": id, "approved_by": "someone-else",
	})
	if code == http.StatusOK {
		t.Fatalf("a declined action was approved afterwards: %s", body)
	}
	// A conflict between two peoples decisions, not a server fault. A 500 tells
	// the caller to retry something that can never succeed.
	if code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", code, body)
	}
}

// The decline has to reach the audit chain with its reason. "A person refused
// this, here is who and why" is the half of the record that did not exist.
func TestADeclineIsRecordedWithItsReason(t *testing.T) {
	app := demoApp(t, []policy.AgentIdentity{
		{AgentID: "other", KeyHash: policy.HashAgentToken("unrelated")},
	})
	id := heldAction(t, app)

	const reason = "the recipient is outside the approved domain"
	if code, body := post(t, app, "/api/responses/decline", map[string]string{
		"action_id": id, "reason": reason,
	}); code != http.StatusOK {
		t.Fatalf("declining failed: %d %s", code, body)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reading the audit trail: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "responses.decline") {
		t.Fatal("the decline is not in the audit trail")
	}
	if !strings.Contains(body, reason) {
		t.Fatalf("the decline was recorded without its reason: %s", body)
	}
}

// Reading the queue is one thing; emptying it is another. An analyst who can
// see held calls must not be able to discard them.
func TestDecliningRequiresOperatorRole(t *testing.T) {
	app := demoApp(t, []policy.AgentIdentity{
		{AgentID: "other", KeyHash: policy.HashAgentToken("unrelated")},
	})
	id := heldAction(t, app)

	encoded, _ := json.Marshal(map[string]string{"action_id": id, "reason": "no"})
	req := httptest.NewRequest(http.MethodPost, "/api/responses/decline", strings.NewReader(string(encoded)))
	req.Header.Set("Authorization", "Bearer analyst-token")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("an unauthorised caller declined a held action: %s", rec.Body.String())
	}
}
