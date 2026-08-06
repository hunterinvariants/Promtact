package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/domain"
)

func TestMetricsExposeOperationalStateWithoutTenantLabels(t *testing.T) {
	app, err := NewWithOptions(Options{Users: []auth.UserConfig{{Name: "viewer", Tenant: "secret-tenant", TokenHash: auth.HashToken("token"), Roles: []string{auth.RoleViewer}}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Routes()
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("metrics must require authentication, got %d", unauthorized.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
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
