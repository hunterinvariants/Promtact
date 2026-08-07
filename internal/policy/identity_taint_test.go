package policy

import (
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// Who is calling is not a statement about what the call contains.
//
// This was found by connecting a real MCP client rather than by reading the
// code. Authenticating with an API token names the principal "legacy-token",
// "token" is on the list of secret terms, and the taint analysis searched the
// actor field along with the payload — so every call from that account was held
// for approval as carrying credential material, including an argument-free
// directory listing. The client sat waiting on approvals and timed out, which
// looked like a network fault and was a policy fault.

func TestIdentityNamesDoNotTaintTheCall(t *testing.T) {
	engine := New(Config{ApprovedTools: []string{"list_documents"}})

	for _, actor := range []string{"legacy-token", "vault-service", "secret-rotator", "password-manager-bot"} {
		decision := engine.GateToolCall(domain.ToolCallRequest{
			ToolName: "list_documents",
			Command:  "list the documents",
			Actor:    actor,
			Hostname: actor + "-host",
			Metadata: map[string]string{"session_id": "identity-" + actor},
		})
		if decision.Verdict != domain.GatewayAllow {
			t.Errorf("an account named %q had its own call held: %s (%s)",
				actor, decision.Verdict, decision.Reason)
		}
		if sources := decision.Metadata["taint_sources"]; strings.Contains(sources, "secret:") {
			t.Errorf("actor %q produced secret taint from its name alone: %s", actor, sources)
		}
	}
}

func TestSecretsInTheCallItselfStillTaint(t *testing.T) {
	engine := New(Config{ApprovedTools: []string{"list_documents"}})

	// The narrowing must not cost the real case: a credential in the arguments
	// is content, and content is exactly what should be examined.
	decision := engine.GateToolCall(domain.ToolCallRequest{
		ToolName:  "list_documents",
		Command:   "list the documents",
		Arguments: "api_key=9f2c4b7ae1d8630fa5c2",
		Actor:     "ordinary-agent",
		Metadata:  map[string]string{"session_id": "identity-content"},
	})
	if decision.Verdict == domain.GatewayAllow {
		t.Error("a credential in the call's own arguments was allowed through unremarked")
	}
}

func TestIdentityIsStillRecordedAsProvenance(t *testing.T) {
	engine := New(Config{ApprovedTools: []string{"list_documents"}})

	decision := engine.GateToolCall(domain.ToolCallRequest{
		ToolName: "list_documents",
		Command:  "list the documents",
		Actor:    "legacy-token",
		Metadata: map[string]string{"session_id": "identity-provenance"},
	})
	// Identity still travels. It says who, which is what it is evidence of.
	if provenance := decision.Metadata["taint_provenance"]; !strings.Contains(provenance, "legacy-token") {
		t.Errorf("the actor was dropped from provenance entirely: %q", provenance)
	}
}
