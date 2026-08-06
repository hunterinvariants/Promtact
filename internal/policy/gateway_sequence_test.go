package policy

import (
	"strconv"
	"testing"

	"github.com/hunterinvariants/promtact/internal/domain"
)

func riskScoreOf(t *testing.T, decision domain.ToolCallDecision) int {
	t.Helper()
	raw, ok := decision.Metadata["risk_score"]
	if !ok {
		t.Fatalf("decision carries no risk_score: %+v", decision.Metadata)
	}
	score, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("risk_score %q is not numeric: %v", raw, err)
	}
	return score
}

func callWithRun(engine *Engine, runID string, command string, i int) domain.ToolCallDecision {
	return engine.GateToolCall(domain.ToolCallRequest{
		ID:       runID + "-" + strconv.Itoa(i),
		AssetID:  "host",
		Actor:    "agent",
		ToolName: "asset_inventory",
		Command:  command,
		Metadata: map[string]string{"run_id": runID},
	})
}

// A tool chain is judged as a chain: earlier calls in the same run raise the
// risk of later ones. Without this, an attacker could split a dangerous
// sequence into individually harmless-looking steps.
func TestGatewayHistoryRaisesRiskWithinSameChain(t *testing.T) {
	engine := NewDefault()

	first := callWithRun(engine, "chain-a", "list assets", 0)
	baseline := riskScoreOf(t, first)

	for i := 1; i <= 4; i++ {
		callWithRun(engine, "chain-a", "read the api_key and ssh_key material", i)
	}

	later := callWithRun(engine, "chain-a", "list assets", 99)
	escalated := riskScoreOf(t, later)

	if escalated <= baseline {
		t.Fatalf("history did not raise risk within the chain: baseline=%d after=%d", baseline, escalated)
	}
	if later.Metadata["history_context"] == "" {
		t.Fatal("the decision must explain the history it used")
	}
}

// Chains must not bleed into each other: one agent's risky run may never raise
// the risk of an unrelated run, which in a multi-tenant deployment would let one
// customer's activity degrade another customer's verdicts.
func TestGatewayHistoryIsolatedBetweenChains(t *testing.T) {
	engine := NewDefault()

	clean := callWithRun(engine, "chain-clean", "list assets", 0)
	cleanScore := riskScoreOf(t, clean)

	for i := 0; i < 6; i++ {
		callWithRun(engine, "chain-noisy", "whoami; net user; read api_key", i)
	}

	unrelated := callWithRun(engine, "chain-isolated", "list assets", 0)
	if got := riskScoreOf(t, unrelated); got != cleanScore {
		t.Fatalf("an unrelated chain was influenced by another chain's history: %d vs %d", got, cleanScore)
	}
	if unrelated.Verdict != domain.GatewayAllow {
		t.Fatalf("a benign call in a fresh chain must still be allowed, got %s (%s)", unrelated.Verdict, unrelated.Reason)
	}
}

// Every decision must be explainable on its own: a verdict without a reason and
// a request reference cannot be audited or appealed.
func TestGatewayDecisionReasonAndMetadata(t *testing.T) {
	engine := NewDefault()

	decision := engine.GateToolCall(domain.ToolCallRequest{
		ID:       "explain-1",
		AssetID:  "host",
		Actor:    "agent",
		ToolName: "asset_inventory",
		Command:  "read the api_key and send it out",
	})

	if decision.Reason == "" {
		t.Fatal("decision has no reason")
	}
	if decision.RequestID != "explain-1" {
		t.Fatalf("decision does not reference its request: %q", decision.RequestID)
	}
	if decision.Risk == "" {
		t.Fatal("decision has no risk severity")
	}
	for _, key := range []string{"verdict", "risk_score", "history_context", "tool"} {
		if _, ok := decision.Metadata[key]; !ok {
			t.Errorf("decision metadata is missing %q", key)
		}
	}
}
