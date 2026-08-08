package policy

import (
	"fmt"
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// A session's past must not lock it out of its own future.
//
// The history contributions could reach 86 points against an inline threshold
// of 70, and none of them decayed. A long-lived agent identity therefore
// climbed past the line and stayed there: every later call was held, including
// an argument-free directory listing, and the gateway simply grew stricter
// with no reason anyone could state. It surfaced as a demonstration that had
// worked an hour earlier and no longer did.

func TestBusySessionIsNotLockedOutByItsOwnHistory(t *testing.T) {
	engine := New(Config{ApprovedTools: []string{"list_documents"}})

	call := func() domain.ToolCallDecision {
		return engine.GateToolCall(domain.ToolCallRequest{
			ToolName: "list_documents",
			Command:  "list the documents",
			Actor:    "long-lived-agent",
			Metadata: map[string]string{"session_id": "busy"},
		})
	}

	for i := 0; i < 60; i++ {
		if decision := call(); decision.Verdict != domain.GatewayAllow {
			t.Fatalf("call %d was held on history alone: %s (%s) risk_factors=%s",
				i+1, decision.Verdict, decision.Reason, decision.Metadata["risk_factors"])
		}
	}
}

func TestHistoryResetsAfterTheSessionGoesIdle(t *testing.T) {
	engine := New(Config{
		ApprovedTools:          []string{"list_documents"},
		UntrustedContentWindow: 20 * time.Millisecond,
	})

	request := domain.ToolCallRequest{
		ToolName: "list_documents",
		Command:  "list the documents",
		Actor:    "idle-agent",
		Metadata: map[string]string{"session_id": "idle"},
	}
	for i := 0; i < 5; i++ {
		engine.GateToolCall(request)
	}

	busy := engine.GateToolCall(request)
	if !hasFactorPrefix(busy.Metadata["risk_factors"], "history:calls=") {
		t.Fatal("the fixture produced no history at all, so the reset below proves nothing")
	}

	time.Sleep(40 * time.Millisecond)

	// A session idle for longer than the window is over. Its counters describe
	// work that finished and must not be charged to whatever comes next.
	after := engine.GateToolCall(request)
	if hasFactorPrefix(after.Metadata["risk_factors"], "history:calls=") {
		t.Errorf("history survived the idle window: %s", after.Metadata["risk_factors"])
	}
}

func TestHistoryCannotDecideAVerdictAlone(t *testing.T) {
	engine := New(Config{ApprovedTools: []string{"list_documents"}})

	// Drive the history as hard as it goes, then check the cap holds.
	for i := 0; i < 40; i++ {
		engine.GateToolCall(domain.ToolCallRequest{
			ToolName: "unapproved_tool",
			Command:  fmt.Sprintf("attempt %d", i),
			Actor:    "noisy-agent",
			Metadata: map[string]string{"session_id": "noisy"},
		})
	}

	decision := engine.GateToolCall(domain.ToolCallRequest{
		ToolName: "list_documents",
		Command:  "list the documents",
		Actor:    "noisy-agent",
		Metadata: map[string]string{"session_id": "noisy"},
	})
	// History is context. It may tip a borderline call; it may not manufacture
	// a verdict for an ordinary one.
	if decision.Verdict == domain.GatewayAllow {
		return
	}
	if decision.Reason == "risk score exceeded the inline allow threshold" {
		t.Errorf("history alone crossed the threshold: %s", decision.Metadata["risk_factors"])
	}
}

func hasFactorPrefix(factors string, prefix string) bool {
	for _, factor := range splitFactors(factors) {
		if len(factor) >= len(prefix) && factor[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func splitFactors(factors string) []string {
	var out []string
	current := ""
	for _, r := range factors {
		if r == ';' {
			out = append(out, current)
			current = ""
			continue
		}
		current += string(r)
	}
	return append(out, current)
}
