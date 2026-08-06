package server

import (
	"encoding/json"
	"testing"

	"github.com/hunterinvariants/promtact/internal/domain"
	"github.com/hunterinvariants/promtact/internal/policy"
)

// Provenance must reach the policy engine from every surface an agent can use,
// not only the direct HTTP API. Otherwise a client could evade a pinned tool
// fingerprint simply by calling through MCP instead.
func TestMCPCarriesToolProvenance(t *testing.T) {
	app := &App{}

	fromMeta := app.toolCallFromMCPRequest(mcpJSONRPCRequest{
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"siem_search","_meta":{"tool_fingerprint":"sha256:good","tool_publisher":"acme"}}`),
	})
	if fromMeta.ToolFingerprint != "sha256:good" || fromMeta.ToolPublisher != "acme" {
		t.Fatalf("provenance from _meta was not carried: %+v", fromMeta)
	}

	fromTopLevel := app.toolCallFromMCPRequest(mcpJSONRPCRequest{
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"siem_search","tool_fingerprint":"sha256:good"}`),
	})
	if fromTopLevel.ToolFingerprint != "sha256:good" {
		t.Fatalf("top-level provenance fallback was not carried: %+v", fromTopLevel)
	}

	absent := app.toolCallFromMCPRequest(mcpJSONRPCRequest{
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"siem_search"}`),
	})
	if absent.ToolFingerprint != "" || absent.ToolPublisher != "" {
		t.Fatalf("a call without provenance must not invent one: %+v", absent)
	}
}

// End to end across surfaces: the same pinned tool is allowed with the right
// fingerprint, denied with a wrong one and gated when it is missing, whether the
// call arrives as a plain tool call or through MCP.
func TestProvenanceEnforcedOnEverySurface(t *testing.T) {
	engine := policy.New(policy.Config{
		ApprovedTools: []string{"siem_search"},
		ToolProvenance: []policy.ToolProvenanceEntry{
			{Tool: "siem_search", Publisher: "acme", Fingerprint: "sha256:good"},
		},
	})
	app := &App{}

	mcpCall := func(fingerprint string) domain.ToolCallRequest {
		params := `{"name":"siem_search","arguments":"q","_meta":{"tool_publisher":"acme"}}`
		if fingerprint != "" {
			params = `{"name":"siem_search","arguments":"q","_meta":{"tool_fingerprint":"` + fingerprint + `","tool_publisher":"acme"}}`
		}
		return app.toolCallFromMCPRequest(mcpJSONRPCRequest{Method: "tools/call", Params: json.RawMessage(params)})
	}

	directCall := func(fingerprint string) domain.ToolCallRequest {
		return domain.ToolCallRequest{
			AssetID: "h", Actor: "agent", ToolName: "siem_search", Command: "q",
			ToolFingerprint: fingerprint, ToolPublisher: "acme",
		}
	}

	for name, build := range map[string]func(string) domain.ToolCallRequest{
		"mcp":    mcpCall,
		"direct": directCall,
	} {
		if verdict := engine.GateToolCall(build("sha256:good")).Verdict; verdict != domain.GatewayAllow {
			t.Errorf("%s: a verified tool should be allowed, got %s", name, verdict)
		}
		if verdict := engine.GateToolCall(build("sha256:WRONG")).Verdict; verdict != domain.GatewayDeny {
			t.Errorf("%s: a mismatched fingerprint must be denied, got %s", name, verdict)
		}
		if verdict := engine.GateToolCall(build("")).Verdict; verdict != domain.GatewayRequireApproval {
			t.Errorf("%s: a missing fingerprint must require approval, got %s", name, verdict)
		}
	}
}
