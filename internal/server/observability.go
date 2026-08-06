package server

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

func (a *App) recordDecisionMetric(verdict domain.GatewayVerdict) {
	a.gatewayMu.Lock()
	defer a.gatewayMu.Unlock()
	switch verdict {
	case domain.GatewayAllow:
		a.gatewayAllowed++
	case domain.GatewayDeny:
		a.gatewayDenied++
	case domain.GatewayRequireApproval:
		a.gatewayQueued++
	}
}

func (a *App) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	a.gatewayMu.Lock()
	samples := append([]time.Duration(nil), a.gatewaySamples...)
	allowed, denied, queued, rejected := a.gatewayAllowed, a.gatewayDenied, a.gatewayQueued, a.gatewayRejected
	a.gatewayMu.Unlock()

	buckets := []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintln(w, "# HELP promtact_gateway_decisions_total Tool decisions by verdict.")
	fmt.Fprintln(w, "# TYPE promtact_gateway_decisions_total counter")
	fmt.Fprintf(w, "promtact_gateway_decisions_total{verdict=\"allow\"} %d\n", allowed)
	fmt.Fprintf(w, "promtact_gateway_decisions_total{verdict=\"deny\"} %d\n", denied)
	fmt.Fprintf(w, "promtact_gateway_decisions_total{verdict=\"queue\"} %d\n", queued)
	fmt.Fprintln(w, "# HELP promtact_gateway_inflight Current in-flight gateway operations.")
	fmt.Fprintln(w, "# TYPE promtact_gateway_inflight gauge")
	fmt.Fprintf(w, "promtact_gateway_inflight %d\n", a.gatewayInFlight())
	fmt.Fprintln(w, "# HELP promtact_gateway_rejected_total Requests rejected by gateway backpressure.")
	fmt.Fprintln(w, "# TYPE promtact_gateway_rejected_total counter")
	fmt.Fprintf(w, "promtact_gateway_rejected_total %d\n", rejected)
	fmt.Fprintln(w, "# HELP promtact_gateway_decision_duration_seconds Gateway decision latency.")
	fmt.Fprintln(w, "# TYPE promtact_gateway_decision_duration_seconds histogram")
	for _, bucket := range buckets {
		count := 0
		for _, sample := range samples {
			if sample.Seconds() <= bucket {
				count++
			}
		}
		fmt.Fprintf(w, "promtact_gateway_decision_duration_seconds_bucket{le=\"%g\"} %d\n", bucket, count)
	}
	fmt.Fprintf(w, "promtact_gateway_decision_duration_seconds_bucket{le=\"+Inf\"} %d\n", len(samples))
	var sum float64
	for _, sample := range samples {
		sum += sample.Seconds()
	}
	fmt.Fprintf(w, "promtact_gateway_decision_duration_seconds_sum %g\n", sum)
	fmt.Fprintf(w, "promtact_gateway_decision_duration_seconds_count %d\n", len(samples))

	chain := a.store.AuditChain()
	valid := 0
	if chain.Valid {
		valid = 1
	}
	fmt.Fprintln(w, "# HELP promtact_audit_chain_valid Whether the audit chain currently validates.")
	fmt.Fprintln(w, "# TYPE promtact_audit_chain_valid gauge")
	fmt.Fprintf(w, "promtact_audit_chain_valid %d\n", valid)
	if stats, ok := a.store.DatabaseStats(); ok {
		fmt.Fprintln(w, "# TYPE promtact_postgres_connections_open gauge")
		fmt.Fprintf(w, "promtact_postgres_connections_open %d\n", stats.OpenConnections)
		fmt.Fprintln(w, "# TYPE promtact_postgres_connections_in_use gauge")
		fmt.Fprintf(w, "promtact_postgres_connections_in_use %d\n", stats.InUse)
		fmt.Fprintln(w, "# TYPE promtact_postgres_connections_idle gauge")
		fmt.Fprintf(w, "promtact_postgres_connections_idle %d\n", stats.Idle)
		fmt.Fprintln(w, "# TYPE promtact_postgres_wait_total counter")
		fmt.Fprintf(w, "promtact_postgres_wait_total %d\n", stats.WaitCount)
	}
}
