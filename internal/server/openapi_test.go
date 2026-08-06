package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The specification must describe the deployment it ships with, so it is served
// from the binary and reachable at the versioned path integrators will use.
func TestOpenAPISpecIsServed(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Routes()

	for _, path := range []string{"/api/openapi.json", "/api/v1/openapi.json"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s returned %d", path, rec.Code)
		}
		var spec map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
			t.Fatalf("%s did not serve valid JSON: %v", path, err)
		}
		if spec["openapi"] == nil || spec["paths"] == nil {
			t.Fatalf("%s is not an OpenAPI document", path)
		}
	}
}

// The document must stay honest about the surface it claims to describe: an
// endpoint that silently disappears from the spec is worse than one that was
// never documented, because integrators trust it.
func TestOpenAPICoversTheEnforcementSurface(t *testing.T) {
	var spec struct {
		Paths      map[string]any `json:"paths"`
		Components struct {
			SecuritySchemes map[string]any `json:"securitySchemes"`
			Schemas         map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPISpec, &spec); err != nil {
		t.Fatalf("embedded spec is not valid JSON: %v", err)
	}

	for _, path := range []string{"/gateway/decide", "/mcp/proxy", "/admin/tenants", "/admin/tenants/{tenant}/usage"} {
		if _, ok := spec.Paths[path]; !ok {
			t.Errorf("the spec does not document %s", path)
		}
	}
	for _, schema := range []string{"ToolCallRequest", "ToolCallDecision", "Alert"} {
		if _, ok := spec.Components.Schemas[schema]; !ok {
			t.Errorf("the spec does not define %s", schema)
		}
	}
	if _, ok := spec.Components.SecuritySchemes["bearerAuth"]; !ok {
		t.Error("the spec must declare how callers authenticate")
	}
}
