package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/demotools"
	"github.com/hunterinvariants/promtact/internal/policy"
)

// The demonstration has to survive the configuration it is demonstrated on.
//
// Registering agent identities is the recommended setup, and it is what a real
// deployment does. With even one registered, every unidentified call is held -
// which is the control working. The demonstration presented no identity, so its
// first tool call was held and the run failed with "unidentified agent requires
// operator approval", on the page that exists to prove the product works.
//
// Nothing caught it. Every check asked whether the demonstration was available,
// and it was; it simply could not complete. So the test runs it.
func demoApp(t *testing.T, identities []policy.AgentIdentity) *App {
	t.Helper()
	tools, err := demotools.New(t.TempDir())
	if err != nil {
		t.Fatalf("preparing demo tools: %v", err)
	}
	if err := tools.Seed(); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	upstream, stop, err := tools.Listen(0)
	if err != nil {
		t.Fatalf("starting the tool server: %v", err)
	}
	t.Cleanup(stop)

	app, err := NewWithOptions(Options{
		Users: []auth.UserConfig{{
			Name:      "operator",
			TokenHash: auth.HashToken("secret"),
			Roles:     []string{auth.RoleAdmin},
		}},
		ProxyAllowLocalTargets: true,
		MCPUpstreamURL:         upstream,
		DemoTools:              tools,
		Policy: policy.Config{
			ApprovedTools:   demotools.Tools,
			AgentIdentities: identities,
		},
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app
}

func runDemo(t *testing.T, app *App) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/demo/agent-run",
		strings.NewReader(`{"via":"gateway"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// The case that was broken in production.
func TestDemonstrationRunsWhenAgentIdentitiesAreConfigured(t *testing.T) {
	app := demoApp(t, []policy.AgentIdentity{
		{AgentID: "some-other-agent", KeyHash: policy.HashAgentToken("unrelated")},
	})

	code, body := runDemo(t, app)
	if code != http.StatusOK {
		t.Fatalf("the demonstration failed with identities configured: %d %s", code, body)
	}
	if strings.Contains(body, "unidentified agent") {
		t.Fatalf("the demonstration still presents no identity: %s", body)
	}

	var result struct {
		Steps []struct {
			Tool    string `json:"tool"`
			Outcome string `json:"outcome"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("decoding: %v (%s)", err, body)
	}
	if len(result.Steps) == 0 {
		t.Fatal("the run produced no steps, so it did not actually execute")
	}
	// The first step must be allowed: that is the one that used to be held, and
	// a run that reports steps without reaching an allowed call proves nothing.
	if result.Steps[0].Outcome != "allowed" {
		t.Fatalf("the first step was %q, want allowed - the run is being gated at the start",
			result.Steps[0].Outcome)
	}
}

// The demonstration identity must not turn identity enforcement on for everyone
// else. A deployment that registered none should keep requiring none, or
// starting with --demo-tools would quietly apply a policy nobody wrote.
func TestTheDemonstrationIdentityDoesNotEnforceIdentityElsewhere(t *testing.T) {
	app := demoApp(t, nil)

	// An ordinary MCP call carrying no identity at all.
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_documents","arguments":{}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/proxy", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "unidentified agent") {
		t.Fatalf("registering the demonstration identity started requiring identities "+
			"from every caller: %s", rec.Body.String())
	}
}

// A caller claiming the demonstration's agent id without its token must not be
// accepted. The identity is real, so it has to behave like one.
func TestTheDemonstrationIdentityCannotBeClaimedWithoutItsToken(t *testing.T) {
	app := demoApp(t, []policy.AgentIdentity{
		{AgentID: "some-other-agent", KeyHash: policy.HashAgentToken("unrelated")},
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_documents","arguments":{}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/proxy", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Promtact-Agent-Id", app.demoAgentID)
	req.Header.Set("X-Promtact-Agent-Token", "not-the-right-token")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("a call claiming the demonstration identity with a wrong token was allowed: %s",
			rec.Body.String())
	}
}
