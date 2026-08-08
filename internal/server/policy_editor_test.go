package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
)

// A customer has to be able to see and change which tools their agents may
// call. It is the central setting of this product, and it lived in a file only
// root could edit.
//
// These tests are mostly about what must not happen: the endpoint sees the
// whole policy document, including user records and key hashes, and it has no
// business showing or rewriting any of it.

func policyApp(t *testing.T) (*App, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.json")
	original := `{
  "approved_tools": ["asset_inventory"],
  "approved_egress_hosts": ["github.com"],
  "users": [
    {"name": "operator", "token_sha256": "` + auth.HashToken("op") + `", "roles": ["operator"]},
    {"name": "analyst", "token_sha256": "` + auth.HashToken("an") + `", "roles": ["analyst"]}
  ],
  "agent_identities": [{"agent_id": "soc-agent", "key_sha256": "abc123"}]
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	app, err := NewWithOptions(Options{
		PolicyPath: path,
		Users: []auth.UserConfig{
			{Name: "operator", TokenHash: auth.HashToken("op"), Roles: []string{auth.RoleOperator}},
			{Name: "analyst", TokenHash: auth.HashToken("an"), Roles: []string{auth.RoleAnalyst}},
		},
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app, path
}

func policyRequest(t *testing.T, app *App, method string, token string, body string) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/api/policy", reader)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

func TestPolicyViewNeverExposesCredentials(t *testing.T) {
	app, _ := policyApp(t)

	code, body := policyRequest(t, app, http.MethodGet, "an", "")
	if code != http.StatusOK {
		t.Fatalf("reading the policy returned %d: %v", code, body)
	}

	// The endpoint reads a file containing user records and key hashes. None of
	// it may reach a browser, whatever role the reader holds.
	raw, _ := json.Marshal(body)
	for _, forbidden := range []string{"token_sha256", "key_sha256", auth.HashToken("op"), "abc123"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("the policy view leaked %q", forbidden)
		}
	}
	// The agent is still named, because knowing which agents are registered is
	// the point; only its secret is withheld.
	if !strings.Contains(string(raw), "soc-agent") {
		t.Error("registered agents are not listed at all")
	}
}

func TestPolicyUpdateKeepsEverythingElseInTheFile(t *testing.T) {
	app, path := policyApp(t)

	code, _ := policyRequest(t, app, http.MethodPut, "op",
		`{"approved_tools":["asset_inventory","send_message"],"approved_egress_hosts":["github.com"]}`)
	if code != http.StatusOK {
		t.Fatalf("updating the policy returned %d", code)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("the written policy is not valid JSON: %v", err)
	}

	// Users and agent identities were never shown and must never be touched.
	users, _ := document["users"].([]any)
	if len(users) != 2 {
		t.Errorf("the update rewrote the user list: %d users left", len(users))
	}
	identities, _ := document["agent_identities"].([]any)
	if len(identities) != 1 {
		t.Errorf("the update rewrote the agent identities: %d left", len(identities))
	}
	tools, _ := document["approved_tools"].([]any)
	if len(tools) != 2 {
		t.Errorf("approved_tools = %v, want two entries", tools)
	}
}

func TestPolicyUpdateRefusesAnEmptyToolList(t *testing.T) {
	app, _ := policyApp(t)

	// An empty list falls back to the built-in defaults rather than approving
	// nothing, so accepting it would silently do the opposite of what somebody
	// clearing the field intends.
	code, _ := policyRequest(t, app, http.MethodPut, "op", `{"approved_tools":[]}`)
	if code != http.StatusBadRequest {
		t.Errorf("an empty tool list returned %d, want 400", code)
	}
}

func TestAnalystCannotChangeThePolicy(t *testing.T) {
	app, path := policyApp(t)
	before, _ := os.ReadFile(path)

	code, _ := policyRequest(t, app, http.MethodPut, "an", `{"approved_tools":["anything"]}`)
	if code == http.StatusOK {
		t.Error("an analyst changed which tools agents may call")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("the policy file changed despite the request being refused")
	}
}

func TestPolicyUpdateRecordsWhatChanged(t *testing.T) {
	app, _ := policyApp(t)

	policyRequest(t, app, http.MethodPut, "op",
		`{"approved_tools":["asset_inventory","send_message"],"approved_egress_hosts":[]}`)

	found := false
	for _, entry := range app.store.ListAudits() {
		if entry.Action != "policy.update" {
			continue
		}
		found = true
		// "Policy updated" answers nothing. Which tools were added, and by
		// whom, is the question an auditor is actually asking.
		if entry.Metadata["added"] != "send_message" {
			t.Errorf("added = %q, want send_message", entry.Metadata["added"])
		}
		if entry.Actor == "" {
			t.Error("the record does not say who changed it")
		}
	}
	if !found {
		t.Error("no policy.update record was written")
	}
}
