package policy

import (
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// The chain, which is where indirect prompt injection actually lives.
//
// Both halves are innocent on their own. Reading a page is what a research
// agent is for. Sending a message is a tool somebody deliberately granted it.
// Judged one call at a time, an injection is two permitted actions in a row and
// every gate passes it.
//
// These tests hold the join.

func sessionCall(session, tool, command, destination string) domain.ToolCallRequest {
	return domain.ToolCallRequest{
		AssetID:     "agent-host",
		Actor:       "research-agent",
		ToolName:    tool,
		Command:     command,
		Destination: destination,
		Metadata:    map[string]string{"session_id": session},
	}
}

func TestUntrustedContentHoldsTheNextOutwardAction(t *testing.T) {
	engine := New(Config{ApprovedTools: []string{"asset_inventory", "ticket_create"}})

	// Before reading anything, the outward action is permitted. This is the
	// control case, and it matters: if the action were held regardless, the
	// test below would prove nothing.
	before := engine.GateToolCall(sessionCall("s-1", "ticket_create", "post the summary to the internal tracker", ""))
	if before.Verdict != domain.GatewayAllow {
		// Asserted rather than assumed. The first version of this test used an
		// external destination, which an older rule already held for approval -
		// so it passed while proving nothing about the mark it was written for.
		t.Fatalf("the baseline action is already held (%s: %s), so this test cannot show what the mark adds",
			before.Verdict, before.Reason)
	}

	// The agent reads a page. Nothing is detected in it - deliberately, because
	// the point is that detection is not what carries this.
	fetch := sessionCall("s-1", "asset_inventory", "read the vendor status page", "https://status.vendor.example")
	inspection := engine.InspectToolResult(fetch, "All systems operational. Last updated 09:14 UTC.")
	if len(inspection.Findings) != 0 {
		t.Fatalf("the fixture is meant to be clean, but findings fired: %v", inspection.Findings)
	}
	engine.RecordToolResultTaint(fetch, inspection.Taint)

	after := engine.GateToolCall(sessionCall("s-1", "ticket_create", "post the summary to the internal tracker", ""))

	if after.Verdict == domain.GatewayAllow {
		t.Fatal("an outward action after reading untrusted content was allowed")
	}
	if after.Reason == "" {
		t.Error("the verdict carries no reason, so an operator cannot tell why this was held")
	}
}

func TestUntrustedContentDoesNotHoldUnrelatedSessions(t *testing.T) {
	engine := New(Config{ApprovedTools: []string{"asset_inventory", "ticket_create"}})

	fetch := sessionCall("s-1", "asset_inventory", "read a page", "https://status.vendor.example")
	engine.RecordToolResultTaint(fetch, engine.InspectToolResult(fetch, "ordinary content").Taint)

	// A different conversation has read nothing and must be unaffected. Taint
	// that leaks across sessions would hold everything, everywhere, which is
	// indistinguishable from a broken product.
	other := engine.GateToolCall(sessionCall("s-2", "ticket_create", "post the summary to the internal tracker", ""))

	if other.Verdict != domain.GatewayAllow {
		t.Errorf("an untouched session was held: %s (%s)", other.Verdict, other.Reason)
	}
}

func TestUntrustedContentDoesNotHoldLocalWork(t *testing.T) {
	engine := New(Config{ApprovedTools: []string{"asset_inventory"}})

	fetch := sessionCall("s-3", "asset_inventory", "read a page", "https://status.vendor.example")
	engine.RecordToolResultTaint(fetch, engine.InspectToolResult(fetch, "ordinary content").Taint)

	// Having read a web page does not make every later action suspect. Only
	// reaching outward does - a control that holds an agent's own local work
	// after one fetch is one somebody turns off.
	local := engine.GateToolCall(sessionCall("s-3", "asset_inventory", "list assets", ""))

	if local.Verdict != domain.GatewayAllow {
		t.Errorf("ordinary local work was held after a fetch: %s (%s)", local.Verdict, local.Reason)
	}
}

func TestUntrustedContentMarkExpires(t *testing.T) {
	engine := New(Config{
		ApprovedTools:          []string{"asset_inventory", "ticket_create"},
		UntrustedContentWindow: time.Millisecond,
	})

	fetch := sessionCall("s-4", "asset_inventory", "read a page", "https://status.vendor.example")
	engine.RecordToolResultTaint(fetch, engine.InspectToolResult(fetch, "ordinary content").Taint)
	time.Sleep(5 * time.Millisecond)

	// A session does not stay suspect forever. Permanent taint after a single
	// page would put a person in front of every later action in a long-running
	// agent, which is the failure mode that gets a control disabled rather than
	// tuned.
	later := engine.GateToolCall(sessionCall("s-4", "ticket_create", "post the summary to the internal tracker", ""))

	if later.Verdict != domain.GatewayAllow {
		t.Errorf("the mark outlived its window: %s (%s)", later.Verdict, later.Reason)
	}
}
