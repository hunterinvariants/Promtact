package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/domain"
)

func metricsTestApp(t *testing.T) http.Handler {
	t.Helper()
	app, err := NewWithOptions(Options{Users: []auth.UserConfig{
		{Name: "platform", Tenant: "default", TokenHash: auth.HashToken("platform-token"), Roles: []string{auth.RoleAdmin}},
		{Name: "viewer", Tenant: "secret-tenant", TokenHash: auth.HashToken("viewer-token"), Roles: []string{auth.RoleViewer}},
		{Name: "customer-admin", Tenant: "secret-tenant", TokenHash: auth.HashToken("customer-admin-token"), Roles: []string{auth.RoleAdmin}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return app.Routes()
}

func getMetrics(handler http.Handler, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// The counters aggregate every tenant, so they are platform-operator data: a
// customer must not learn the deployment's total traffic, capacity or database
// state — not even a customer whose own role is admin.
func TestMetricsRestrictedToPlatformOperator(t *testing.T) {
	handler := metricsTestApp(t)

	if code := getMetrics(handler, "").Code; code != http.StatusUnauthorized {
		t.Fatalf("metrics must require authentication, got %d", code)
	}
	if code := getMetrics(handler, "viewer-token").Code; code != http.StatusForbidden {
		t.Fatalf("a customer viewer must not read platform metrics, got %d", code)
	}
	if code := getMetrics(handler, "customer-admin-token").Code; code != http.StatusForbidden {
		t.Fatalf("a customer's own admin must not read platform metrics, got %d", code)
	}
}

func TestMetricsExposeOperationalStateWithoutTenantLabels(t *testing.T) {
	handler := metricsTestApp(t)

	rec := getMetrics(handler, "platform-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, metric := range []string{"promtact_gateway_decisions_total", "promtact_gateway_decision_duration_seconds", "promtact_gateway_inflight", "promtact_audit_chain_valid"} {
		if !strings.Contains(body, metric) {
			t.Errorf("missing metric %s", metric)
		}
	}
	if strings.Contains(body, "secret-tenant") || strings.Contains(body, "token") {
		t.Fatal("metrics leaked tenant or credential content")
	}
}

func TestDecisionMetricCounters(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	app.recordDecisionMetric(domain.GatewayAllow)
	app.recordDecisionMetric(domain.GatewayDeny)
	app.recordDecisionMetric(domain.GatewayRequireApproval)
	rec := httptest.NewRecorder()
	app.handleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, line := range []string{"{verdict=\"allow\"} 1", "{verdict=\"deny\"} 1", "{verdict=\"queue\"} 1"} {
		if !strings.Contains(body, line) {
			t.Errorf("missing counter line %q in %s", line, body)
		}
	}
}
