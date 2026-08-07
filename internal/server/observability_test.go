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

// The funnel is the headline of the console, so it must not read zero after a
// deploy. The in-memory counters exist for Prometheus, where a reset at process
// start is expected; the dashboard reads the durable records instead.
func TestDecisionCountsSurviveLosingProcessState(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}

	for _, verdict := range []domain.GatewayVerdict{
		domain.GatewayAllow, domain.GatewayAllow,
		domain.GatewayRequireApproval,
		domain.GatewayDeny,
	} {
		app.recordAudit(nil, auth.Principal{Name: "agent", Tenant: "default"},
			"gateway.decide", "tool_call", "req-1", string(verdict), nil)
		app.recordDecisionMetric(verdict)
	}

	allowed, gated, denied := app.decisionCounts()
	if allowed != 2 || gated != 1 || denied != 1 {
		t.Fatalf("wrong counts: allowed=%d gated=%d denied=%d", allowed, gated, denied)
	}

	// Throw away everything the process holds, as a restart would.
	app.gatewayMu.Lock()
	app.gatewayAllowed, app.gatewayQueued, app.gatewayDenied = 0, 0, 0
	app.gatewayMu.Unlock()

	allowed, gated, denied = app.decisionCounts()
	if allowed != 2 || gated != 1 || denied != 1 {
		t.Fatalf("counts were lost with the process state: allowed=%d gated=%d denied=%d",
			allowed, gated, denied)
	}
}
