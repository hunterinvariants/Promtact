package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/crypto"
	"github.com/hunterinvariants/promtact/internal/domain"
)

// Credential brokering: the gateway holds the key, the agent does not.
//
// The property being tested is not "the header is set". It is that the secret
// travels exactly one way - outward, to the tool - and appears in no response,
// no audit record and no action metadata on the way back. A test that only
// checked the outbound header would pass just as happily while the secret was
// also being echoed into the console.

const brokerSecret = "sk-live-7f3a9c21e4b8d6f0-not-a-real-key"

// recordingUpstream captures what the gateway actually presented, so the
// assertions are about the wire rather than about internal state.
type recordingUpstream struct {
	mu     sync.Mutex
	auths  []string
	server *httptest.Server
}

func newRecordingUpstream(t *testing.T, body string) *recordingUpstream {
	t.Helper()
	upstream := &recordingUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.mu.Lock()
		upstream.auths = append(upstream.auths, r.Header.Get("Authorization"))
		upstream.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *recordingUpstream) presented() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.auths...)
}

func brokerApp(t *testing.T, upstream string, withKey bool) *App {
	t.Helper()
	app, err := NewWithOptions(Options{
		Users: []auth.UserConfig{{
			Name:      "operator",
			TokenHash: auth.HashToken("secret"),
			Roles:     []string{auth.RoleAdmin},
		}},
		ProxyAllowLocalTargets: true,
		MCPUpstreamURL:         upstream,
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if withKey {
		key, err := crypto.GenerateKey()
		if err != nil {
			t.Fatalf("generating a key: %v", err)
		}
		provider, err := crypto.NewLocalKeyProvider("test", map[string]string{"test": key})
		if err != nil {
			t.Fatalf("key provider: %v", err)
		}
		app.store.SetSealer(crypto.NewSealer(provider))
	}
	return app
}

func storeCredential(t *testing.T, app *App, tool string, secret string) map[string]any {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"tool": tool, "secret": secret})
	req := httptest.NewRequest(http.MethodPost, "/api/credentials", strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("storing the credential: %d %s", rec.Code, rec.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	return decoded
}

func brokeredToolCall(t *testing.T, app *App, tool string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":{"q":"x"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/proxy", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	return rec
}

// The credential must reach the tool, and must not come back.
func TestBrokeredCredentialReachesUpstreamAndNothingElse(t *testing.T) {
	upstream := newRecordingUpstream(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`)
	app := brokerApp(t, upstream.server.URL, true)

	stored := storeCredential(t, app, "asset_inventory", brokerSecret)
	fingerprint, _ := stored["fingerprint"].(string)
	if fingerprint == "" {
		t.Fatal("a stored credential must report a fingerprint, or an operator cannot tell what is installed")
	}

	// The write response is the first place a secret could leak back.
	if raw, _ := json.Marshal(stored); strings.Contains(string(raw), brokerSecret) {
		t.Fatalf("the secret came back in the write response: %s", raw)
	}

	rec := brokeredToolCall(t, app, "asset_inventory")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected the call to be allowed, got %d: %s", rec.Code, rec.Body.String())
	}

	presented := upstream.presented()
	if len(presented) == 0 {
		t.Fatal("the call never reached the upstream, so this test proves nothing about the credential")
	}
	if presented[len(presented)-1] != "Bearer "+brokerSecret {
		t.Fatalf("upstream saw %q, want the brokered secret", presented[len(presented)-1])
	}

	// And now the direction that matters: nothing the agent or an operator can
	// read may contain it.
	if strings.Contains(rec.Body.String(), brokerSecret) {
		t.Fatalf("the secret was echoed to the agent: %s", rec.Body.String())
	}

	bodies := map[string]string{}
	for _, surface := range []string{"/api/responses", "/api/audit", "/api/credentials"} {
		read := httptest.NewRequest(http.MethodGet, surface, nil)
		read.Header.Set("Authorization", "Bearer secret")
		readRec := httptest.NewRecorder()
		app.Routes().ServeHTTP(readRec, read)
		// Asserting the status first, because a 404 body contains no secret
		// either and would let this check pass while proving nothing. That is
		// exactly how the first version of this test passed.
		if readRec.Code != http.StatusOK {
			t.Fatalf("%s returned %d, so the leak check would be vacuous", surface, readRec.Code)
		}
		if strings.Contains(readRec.Body.String(), brokerSecret) {
			t.Fatalf("%s exposes the brokered secret", surface)
		}
		bodies[surface] = readRec.Body.String()
	}

	// The fingerprint is what belongs there instead: enough to answer "which
	// credential did the agent use" during an investigation, useless to anyone
	// hoping to reuse it.
	if !strings.Contains(bodies["/api/responses"], fingerprint) {
		t.Errorf("the recorded decision carries no credential fingerprint, so an investigator\n"+
			"cannot tell which credential was used; body was:\n%s", bodies["/api/responses"])
	}
}

// Without an encryption key the secret would sit in plaintext in every backup.
// Refusing is the only safe answer, and it has to be an answer rather than a
// silent downgrade.
func TestCredentialRefusedWithoutEncryptionKey(t *testing.T) {
	upstream := newRecordingUpstream(t, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	app := brokerApp(t, upstream.server.URL, false)

	payload, _ := json.Marshal(map[string]string{"tool": "asset_inventory", "secret": brokerSecret})
	req := httptest.NewRequest(http.MethodPost, "/api/credentials", strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 telling the operator to configure a key, got %d: %s", rec.Code, rec.Body.String())
	}
	// The refusal has to name the variable that actually exists. An earlier
	// version named PROMTACT_ENCRYPTION_KEY, which nothing reads, and this test
	// did not notice because it configures the sealer directly rather than
	// through the environment. So the name is checked against the loader.
	body := rec.Body.String()
	for _, variable := range []string{"PROMTACT_ENCRYPTION_KEYS", "PROMTACT_ENCRYPTION_KEY_ID"} {
		if !strings.Contains(body, variable) {
			t.Errorf("the refusal should name %s, the variable the key loader reads; got %s", variable, body)
		}
	}
	if _, err := crypto.LocalKeyProviderFromEnv(); err != nil {
		t.Fatalf("sanity check on the loader failed: %v", err)
	}
}

// A deployment that has not adopted brokering must keep working. Silently
// breaking every existing install would guarantee nobody adopts this.
func TestUnbrokeredCallFallsBackToStaticToken(t *testing.T) {
	upstream := newRecordingUpstream(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`)
	app := brokerApp(t, upstream.server.URL, true)
	app.mcpUpstreamToken = "legacy-static-token"

	// A credential exists, but for a different tool.
	storeCredential(t, app, "some_other_tool", brokerSecret)

	rec := brokeredToolCall(t, app, "asset_inventory")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected the call to be allowed, got %d: %s", rec.Code, rec.Body.String())
	}
	presented := upstream.presented()
	if len(presented) == 0 {
		t.Fatal("the call never reached the upstream")
	}
	if got := presented[len(presented)-1]; got != "Bearer legacy-static-token" {
		t.Fatalf("upstream saw %q, want the static token as the fallback", got)
	}
}

// Deleting a credential revokes the agent's access without touching the agent.
func TestDeletedCredentialStopsBeingPresented(t *testing.T) {
	upstream := newRecordingUpstream(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`)
	app := brokerApp(t, upstream.server.URL, true)

	stored := storeCredential(t, app, "asset_inventory", brokerSecret)
	id, _ := stored["id"].(string)

	brokeredToolCall(t, app, "asset_inventory")
	if got := upstream.presented(); got[len(got)-1] != "Bearer "+brokerSecret {
		t.Fatalf("precondition failed: the credential was not presented in the first place, got %q", got[len(got)-1])
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/credentials?id="+id, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deleting: %d %s", rec.Code, rec.Body.String())
	}

	brokeredToolCall(t, app, "asset_inventory")
	presented := upstream.presented()
	if got := presented[len(presented)-1]; strings.Contains(got, brokerSecret) {
		t.Fatalf("the credential is still being presented after deletion: %q", got)
	}
}

// The claim itself: a route around the gateway is a dead end.
//
// This is the sentence a buyer will push on, so it is tested as a buyer would
// check it - with a tool that actually enforces its own authentication, and an
// agent that tries to reach it directly using the only credential it holds.
//
// Both halves are asserted. A test that only showed the direct call failing
// would pass if the tool were simply broken, which proves nothing at all.
func TestAgentTokenIsWorthlessAtTheToolButWorksThroughTheGateway(t *testing.T) {
	const agentToken = "secret" // the agent's gateway token, and nothing more

	var reachedTool int
	tool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A tool that checks its own key, like any real one.
		if r.Header.Get("Authorization") != "Bearer "+brokerSecret {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		reachedTool++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))
	defer tool.Close()

	app := brokerApp(t, tool.URL, true)
	storeCredential(t, app, "asset_inventory", brokerSecret)

	// The agent goes around the gateway, presenting what it has.
	direct, err := http.NewRequest(http.MethodPost, tool.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`))
	if err != nil {
		t.Fatalf("building the direct request: %v", err)
	}
	direct.Header.Set("Authorization", "Bearer "+agentToken)
	resp, err := tool.Client().Do(direct)
	if err != nil {
		t.Fatalf("direct call: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the agent reached the tool directly with its gateway token (%d) - "+
			"the credential is not actually being withheld", resp.StatusCode)
	}

	// The same agent, through the gateway, succeeds.
	rec := brokeredToolCall(t, app, "asset_inventory")
	if rec.Code != http.StatusOK {
		t.Fatalf("through the gateway the call should succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	if reachedTool != 1 {
		t.Fatalf("the tool accepted %d authenticated calls, want exactly 1 - "+
			"without this the test above passes even if the tool is simply unreachable", reachedTool)
	}
}

// Selection precedence, as a pure unit: exact beats prefix beats fallback.
// Without an explicit ranking this depends on row order, which is a quiet way
// to hand the wrong tool the wrong key after an unrelated change.
func TestCredentialSelectionPrefersTheMostSpecificPattern(t *testing.T) {
	credentials := []domain.Credential{
		{ID: "c-any", Tenant: "acme", Tool: "*", Secret: "fallback"},
		{ID: "c-prefix", Tenant: "acme", Tool: "github_*", Secret: "prefix"},
		{ID: "c-exact", Tenant: "acme", Tool: "github_create_issue", Secret: "exact"},
		{ID: "c-other", Tenant: "other", Tool: "github_create_issue", Secret: "wrong tenant"},
	}

	for _, tc := range []struct{ tool, want string }{
		{"github_create_issue", "exact"},
		{"github_list_repos", "prefix"},
		{"jira_create", "fallback"},
	} {
		got, ok := domain.SelectCredential(credentials, "acme", tc.tool)
		if !ok {
			t.Fatalf("%s: no credential selected", tc.tool)
		}
		if got.Secret != tc.want {
			t.Errorf("%s: selected %q, want %q", tc.tool, got.Secret, tc.want)
		}
	}

	// A tenant with no credentials must not borrow another tenant's.
	if _, ok := domain.SelectCredential(credentials, "unrelated", "github_create_issue"); ok {
		t.Error("a tenant with no credentials was given one belonging to another tenant")
	}
}
