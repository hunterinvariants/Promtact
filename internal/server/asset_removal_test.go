package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/domain"
)

// Decommissioning a machine, through the application rather than by hand.
//
// The route this replaces was SQL against the database, which fails twice: a
// customer cannot reach the database at all, and an operator who can deletes
// rows underneath a process that holds the same records in memory — so the
// removal appears to do nothing until a restart, and a restart can undo it.

func removalApp(t *testing.T) *App {
	t.Helper()
	app, err := NewWithOptions(Options{
		Users: []auth.UserConfig{
			{Name: "operator", TokenHash: auth.HashToken("op"), Roles: []string{auth.RoleOperator}},
			{Name: "analyst", TokenHash: auth.HashToken("an"), Roles: []string{auth.RoleAnalyst}},
		},
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if _, err := app.ingest([]domain.Event{{
		Kind:     domain.EventProcessStart,
		AssetID:  "laptop-1",
		Hostname: "laptop-1",
		Process:  "whoami.exe",
		Command:  "whoami /groups",
	}}, "default"); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return app
}

func deleteAsset(t *testing.T, app *App, assetID string, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/assets/"+assetID, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

func TestRemovingAnAssetClearsItsRecords(t *testing.T) {
	app := removalApp(t)

	if len(app.store.ListAssets()) == 0 {
		t.Fatal("the fixture produced no asset, so this test would prove nothing")
	}

	code, body := deleteAsset(t, app, "laptop-1", "op")
	if code != http.StatusOK {
		t.Fatalf("delete returned %d: %v", code, body)
	}

	if assets := app.store.ListAssets(); len(assets) != 0 {
		t.Errorf("the asset is still listed: %v", assets)
	}
	if events := app.store.ListEvents(); len(events) != 0 {
		t.Errorf("%d event(s) survived the removal", len(events))
	}
}

func TestRemovingAnAssetLeavesAnAuditRecord(t *testing.T) {
	app := removalApp(t)
	before := len(app.store.ListAudits())

	if code, body := deleteAsset(t, app, "laptop-1", "op"); code != http.StatusOK {
		t.Fatalf("delete returned %d: %v", code, body)
	}

	audits := app.store.ListAudits()
	if len(audits) <= before {
		t.Fatal("nothing was recorded; this is the whole reason the endpoint exists rather than advice to run SQL")
	}
	found := false
	for _, entry := range audits {
		if entry.Action == "asset.remove" {
			found = true
		}
	}
	if !found {
		t.Error("no asset.remove entry among the audit records")
	}
}

func TestRemovingAnAssetKeepsTheAuditChain(t *testing.T) {
	app := removalApp(t)
	before := len(app.store.ListAudits())

	if code, _ := deleteAsset(t, app, "laptop-1", "op"); code != http.StatusOK {
		t.Fatal("delete failed")
	}

	// Audit records outlive what they describe. A chain that can have entries
	// taken out of it is not a chain, so removal must never touch them.
	if after := len(app.store.ListAudits()); after < before {
		t.Errorf("audit records were removed: %d before, %d after", before, after)
	}
}

func TestRemovingAnUnknownAssetIsRefused(t *testing.T) {
	app := removalApp(t)

	// Reporting success for something that was never there would let a typo
	// read as a completed decommissioning.
	code, _ := deleteAsset(t, app, "no-such-machine", "op")
	if code != http.StatusNotFound {
		t.Errorf("removing an unknown asset returned %d, want 404", code)
	}
}

func TestAnalystCannotRemoveAnAsset(t *testing.T) {
	app := removalApp(t)

	code, _ := deleteAsset(t, app, "laptop-1", "an")
	if code == http.StatusOK {
		t.Error("an analyst removed an asset and its history")
	}
	if len(app.store.ListAssets()) == 0 {
		t.Error("the asset was removed despite the request being refused")
	}
}

func TestRemovalReportsWhatItRemoved(t *testing.T) {
	app := removalApp(t)

	_, body := deleteAsset(t, app, "laptop-1", "op")
	removed, ok := body["removed"].(map[string]any)
	if !ok {
		t.Fatalf("no counts in the response: %v", body)
	}
	// "Done" is not an answer when the operator is about to be asked what was
	// deleted.
	if events, _ := removed["events"].(float64); events < 1 {
		t.Errorf("reported %v events removed, expected at least one", removed["events"])
	}
	if note, _ := body["note"].(string); !strings.Contains(note, "Audit") {
		t.Error("the response does not say that audit records were kept")
	}
}
