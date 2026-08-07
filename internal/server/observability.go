package server

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
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
	// These counters aggregate every tenant, so they belong to the platform
	// operator alone: a customer's own admin must not be able to read the
	// deployment's total traffic, capacity or database state.
	if a.authenticationConfigured() && !isPlatformAdmin(principalFromRequest(r)) {
		writeError(w, http.StatusForbidden, errors.New("platform administrator role required"))
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

	degraded, _, _ := a.DegradedState()
	degradedValue := 0
	if degraded {
		degradedValue = 1
	}
	fmt.Fprintln(w, "# HELP promtact_degraded_mode Whether durable persistence is currently failing while enforcement continues.")
	fmt.Fprintln(w, "# TYPE promtact_degraded_mode gauge")
	fmt.Fprintf(w, "promtact_degraded_mode %d\n", degradedValue)
	fmt.Fprintln(w, "# HELP promtact_decision_journal_depth Records awaiting reconciliation into storage.")
	fmt.Fprintln(w, "# TYPE promtact_decision_journal_depth gauge")
	fmt.Fprintf(w, "promtact_decision_journal_depth %d\n", a.journal.Depth())
	fmt.Fprintln(w, "# HELP promtact_decision_journal_dropped_total Records refused because the journal was full.")
	fmt.Fprintln(w, "# TYPE promtact_decision_journal_dropped_total counter")
	fmt.Fprintf(w, "promtact_decision_journal_dropped_total %d\n", a.journal.Dropped())

	if a.tracer.enabled() {
		fmt.Fprintln(w, "# HELP promtact_trace_spans_dropped_total Spans discarded because the export queue was full.")
		fmt.Fprintln(w, "# TYPE promtact_trace_spans_dropped_total counter")
		fmt.Fprintf(w, "promtact_trace_spans_dropped_total %d\n", a.tracer.Dropped())
	}

	if a.witness.enabled() {
		witnessed, diverged, _ := a.witness.status()
		divergedValue := 0
		if diverged {
			divergedValue = 1
		}
		fmt.Fprintln(w, "# HELP promtact_audit_witness_diverged Whether the local audit chain disagrees with the external witness.")
		fmt.Fprintln(w, "# TYPE promtact_audit_witness_diverged gauge")
		fmt.Fprintf(w, "promtact_audit_witness_diverged %d\n", divergedValue)
		fmt.Fprintln(w, "# HELP promtact_audit_witnessed_index Chain index most recently accepted by the external witness.")
		fmt.Fprintln(w, "# TYPE promtact_audit_witnessed_index gauge")
		fmt.Fprintf(w, "promtact_audit_witnessed_index %d\n", witnessed.Index)
	}

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

// assuranceFor reports the deployment-wide picture, and only to the platform
// operator. These numbers aggregate every tenant — total decision volume, the
// health of the evidence trail, whether an operator accessed the database
// unannounced. A customer's own admin must not read them, which is the same
// rule the metrics endpoint enforces.
func (a *App) assuranceFor(principal auth.Principal) *domain.Assurance {
	if a.authenticationConfigured() && !isPlatformAdmin(principal) {
		return nil
	}

	allowed, gated, denied := a.decisionCounts()

	chain := a.store.AuditChain()
	degraded, _, _ := a.DegradedState()
	_, unannounced := a.accessLogSummary()

	assurance := &domain.Assurance{
		DecisionsAllowed: allowed,
		DecisionsGated:   gated,
		DecisionsDenied:  denied,
		DecisionsTotal:   allowed + gated + denied,
		AuditChainValid:  chain.Valid,
		AuditChainIndex:  chain.Linked,
		DegradedMode:     degraded,
		JournalDepth:     a.journal.Depth(),

		UnannouncedSessions: unannounced,
		ShipperSilent:       a.accessLogSilent(),
	}

	if a.witness.enabled() {
		witnessed, diverged, _ := a.witness.status()
		assurance.WitnessConfigured = true
		assurance.WitnessIndex = witnessed.Index
		assurance.WitnessDiverged = diverged
	}
	return assurance
}

// decisionCounts reads the verdicts from the audit records rather than from the
// in-memory counters. Those counters exist for Prometheus, where a reset at
// process start is expected and handled by rate(); on a dashboard it means the
// headline reads zero after every deploy, which is exactly when someone is
// looking at it.
//
// The audit records are durable, and their window is the retention policy — so
// the figure is "decisions we still hold evidence for" rather than "decisions
// since this process started", which is the more useful of the two anyway.
func (a *App) decisionCounts() (allowed int, gated int, denied int) {
	for _, audit := range a.store.ListAudits() {
		if audit.Action != "gateway.decide" {
			continue
		}
		switch domain.GatewayVerdict(audit.Outcome) {
		case domain.GatewayAllow:
			allowed++
		case domain.GatewayRequireApproval:
			gated++
		case domain.GatewayDeny:
			denied++
		}
	}
	return allowed, gated, denied
}
