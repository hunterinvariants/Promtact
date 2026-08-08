package server

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

// The evidence pack.
//
// This is what a provider hands to their customer and what a customer files.
// The question it answers is the one nobody could answer before: what did your
// agents do last quarter, what was stopped, who approved the rest, and why
// should anyone believe the record.
//
// It is assembled from audit records rather than from counters. A count held in
// memory looks right in a test and is wrong after a restart, which this work
// has now been bitten by twice; a report derived from the records cannot
// disagree with the records.
//
// What it deliberately does not do is flatter. A period with nothing in it says
// so, an unwitnessed chain says what that does and does not prove, and the
// closing section lists what this system does not see at all. A report that
// only contains good news is one an auditor stops reading.

type reportPeriod struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	Days int       `json:"days"`
}

type reportCounts struct {
	Decided int `json:"decided"`
	Allowed int `json:"allowed"`
	Held    int `json:"held"`
	Stopped int `json:"stopped"`
}

type reportReason struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type reportApproval struct {
	Tool       string    `json:"tool"`
	Reason     string    `json:"reason"`
	ApprovedBy string    `json:"approved_by"`
	At         time.Time `json:"at"`
}

type reportPolicyChange struct {
	At      time.Time `json:"at"`
	By      string    `json:"by"`
	Added   string    `json:"added,omitempty"`
	Removed string    `json:"removed,omitempty"`
}

type reportIntegrity struct {
	Records           int    `json:"records"`
	Linked            int    `json:"linked"`
	Valid             bool   `json:"valid"`
	Head              string `json:"head"`
	WitnessConfigured bool   `json:"witness_configured"`
	WitnessAgrees     bool   `json:"witness_agrees"`
	WitnessIndex      int    `json:"witness_index"`
	WitnessAt         string `json:"witness_at,omitempty"`
	Statement         string `json:"statement"`
}

type report struct {
	Tenant        string               `json:"tenant"`
	GeneratedAt   time.Time            `json:"generated_at"`
	GeneratedBy   string               `json:"generated_by"`
	Period        reportPeriod         `json:"period"`
	Counts        reportCounts         `json:"counts"`
	StoppedFor    []reportReason       `json:"stopped_for"`
	HeldFor       []reportReason       `json:"held_for"`
	Approvals     []reportApproval     `json:"approvals"`
	PolicyChanges []reportPolicyChange `json:"policy_changes"`
	Integrity     reportIntegrity      `json:"integrity"`
	NotCovered    []string             `json:"not_covered"`
}

func (a *App) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	principal := principalFromRequest(r)
	tenant := tenantForPrincipal(principal)

	to := time.Now().UTC()
	from := to.AddDate(0, 0, -30)
	if value := strings.TrimSpace(r.URL.Query().Get("from")); value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			from = parsed.UTC()
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("to")); value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			to = parsed.UTC()
		}
	}

	audits := a.listAuditsForTenant(tenant)
	chain := a.auditChainForTenant(tenant)
	witness := a.witnessSnapshot(r.Context())

	document := report{
		Tenant:      tenantOrDefault(tenant),
		GeneratedAt: time.Now().UTC(),
		GeneratedBy: principal.Name,
		Period:      reportPeriod{From: from, To: to, Days: int(to.Sub(from).Hours() / 24)},
		NotCovered: []string{
			"The prompt an agent was given, the conversation around it, and the model's reasoning. This gateway sits between an agent and its tools; it records what a tool was asked to do and what was decided.",
			"Anything an agent can reach without passing through the gateway. Where an agent has other routes to the same data, those calls are not here.",
			"Whether a held call was a genuine attack. A hold is a decision to involve a person, not a finding.",
		},
	}

	stopped := map[string]int{}
	held := map[string]int{}

	for _, entry := range audits {
		if entry.Timestamp.Before(from) || entry.Timestamp.After(to) {
			continue
		}
		switch entry.Action {
		case "mcp.proxy", "gateway.decide", "gateway.proxy":
			document.Counts.Decided++
			reason := firstNonEmptyString(entry.Metadata["result_reason"], entry.Metadata["reason"], "not stated")
			switch entry.Outcome {
			case "withheld", "blocked":
				document.Counts.Stopped++
				stopped[reason]++
			case "pending_approval":
				document.Counts.Held++
				held[reason]++
			default:
				document.Counts.Allowed++
			}
		case "response.approve", "gateway.action.approve":
			document.Approvals = append(document.Approvals, reportApproval{
				Tool:       entry.Metadata["tool"],
				Reason:     entry.Metadata["reason"],
				ApprovedBy: firstNonEmptyString(entry.Metadata["approved_by"], entry.Actor),
				At:         entry.Timestamp,
			})
		case "policy.update":
			document.PolicyChanges = append(document.PolicyChanges, reportPolicyChange{
				At:      entry.Timestamp,
				By:      entry.Actor,
				Added:   entry.Metadata["added"],
				Removed: entry.Metadata["removed"],
			})
		}
	}

	document.StoppedFor = sortedReasons(stopped)
	document.HeldFor = sortedReasons(held)

	document.Integrity = reportIntegrity{
		Records:           chain.Total,
		Linked:            chain.Linked,
		Valid:             chain.Valid,
		Head:              chain.Head,
		WitnessConfigured: witness.Configured,
		WitnessAgrees:     witness.Agrees,
		WitnessIndex:      witness.WitnessedIndex,
	}
	if !witness.WitnessedAt.IsZero() {
		document.Integrity.WitnessAt = witness.WitnessedAt.UTC().Format(time.RFC3339)
	}
	// The statement is written here rather than in the browser so that the JSON
	// export and the printed page cannot say different things about the same
	// chain — and so that the honest version is the only version.
	switch {
	case !chain.Valid:
		document.Integrity.Statement = "The record does not verify. A record has been changed or removed, or records were deleted by a retention policy. This must be explained before the rest of this report is relied on."
	case !witness.Configured:
		document.Integrity.Statement = "The record is hash-linked and internally consistent. No external witness is configured, so this detects accidental corruption only: anyone able to write to the database could rewrite every record and recompute every hash."
	case witness.Diverged:
		document.Integrity.Statement = "An external witness holds a version of this record that this server can no longer produce. Something here was removed or rewritten. Investigate before relying on this report."
	case witness.Agrees:
		document.Integrity.Statement = "The record is hash-linked and an external witness outside this server agrees with it. An operator with full access to this server and its database cannot remove a decision from this record without the witness refusing the result."
	default:
		document.Integrity.Statement = "The record is hash-linked. An external witness is configured but has not yet been brought up to date with the newest records."
	}

	writeJSON(w, http.StatusOK, document)
}

func sortedReasons(counts map[string]int) []reportReason {
	out := make([]reportReason, 0, len(counts))
	for reason, count := range counts {
		out = append(out, reportReason{Reason: reason, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
