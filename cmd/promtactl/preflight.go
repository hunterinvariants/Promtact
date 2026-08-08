package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
)

// promtactl preflight - what is not ready, before somebody is watching.
//
// Every awkward moment in a demonstration so far was something that had been
// true an hour earlier and quietly stopped being true: a witness nobody had
// configured on this instance, a demo directory the service could not write, an
// outbox still holding the previous run's message. None of it was hard to
// check. It was hard to remember at the moment when forgetting costs the most.
//
// So it is one command, it reports what is wrong rather than that everything is
// fine, and it exits non-zero so it can gate a script.

type preflightCheck struct {
	name   string
	ok     bool
	detail string
	fix    string
}

func preflightCommand(args []string) error {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	// Running the demonstration is the whole value of this command, so it is on
	// by default. It has one side effect worth knowing about: the guarded run
	// ends with a held call, which lands in the approval queue. Decline it
	// afterwards, or skip the run.
	skipDemo := fs.Bool("skip-demo", false, "do not run the demonstration (it leaves one held call in the queue)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	// Reachable at all. Everything below is meaningless otherwise, so this is a
	// hard stop rather than a failed check.
	if _, status, err := tenantCall(http.MethodGet, base+"/readyz", token, nil); err != nil || status >= 300 {
		fmt.Printf("The deployment at %s is not answering.\n", base)
		if err != nil {
			fmt.Printf("  %v\n", err)
		} else {
			fmt.Printf("  /readyz returned %d - it is up but its storage is not.\n", status)
		}
		return fmt.Errorf("nothing else can be checked until it answers")
	}

	body, status, err := tenantCall(http.MethodGet, base+"/api/status", token, nil)
	if err != nil {
		return fmt.Errorf("reading the status failed: %w", err)
	}
	if status >= 300 {
		return fmt.Errorf("reading the status returned %d: %s", status, strings.TrimSpace(string(body)))
	}

	var state struct {
		Version   string `json:"version"`
		DemoTools bool   `json:"demo_tools"`
		// A pointer, and omitted entirely unless the caller is the platform
		// operator. Treating a missing block as a pile of failed checks would
		// make this command cry wolf at exactly the wrong moment.
		Assurance *struct {
			AuditChainValid     bool   `json:"audit_chain_valid"`
			AuditChainIndex     int    `json:"audit_chain_index"`
			WitnessConfigured   bool   `json:"witness_configured"`
			WitnessDiverged     bool   `json:"witness_diverged"`
			WitnessIndex        int    `json:"witness_index"`
			PolicyLoaded        bool   `json:"policy_loaded"`
			ApprovedTools       int    `json:"approved_tools"`
			SessionMarksDurable bool   `json:"session_marks_durable"`
			SessionMarkError    string `json:"session_mark_error"`
			ApprovalsWaiting    int    `json:"approvals_waiting"`
			MCPUpstream         string `json:"mcp_upstream"`
			DegradedMode        bool   `json:"degraded_mode"`
		} `json:"assurance"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return fmt.Errorf("unexpected status response: %s", strings.TrimSpace(string(body)))
	}

	fmt.Printf("%s\nPromtact %s at %s\n%s\n\n", strings.Repeat("-", 70), state.Version, base, strings.Repeat("-", 70))

	if state.Assurance == nil {
		fmt.Println("This token sees a tenant, not the deployment. The assurance block that")
		fmt.Println("carries the chain, the witness and the policy state is returned only to")
		fmt.Println("the platform operator, so there is nothing here to check.")
		fmt.Println()
		fmt.Println("  Re-run with the operator token, on the host itself:")
		fmt.Println("    promtactl preflight --url http://127.0.0.1:8080 --token <operator token>")
		return fmt.Errorf("not the operator token")
	}
	a := state.Assurance

	var checks []preflightCheck
	add := func(c preflightCheck) { checks = append(checks, c) }

	add(preflightCheck{
		name: "Demonstration page", ok: state.DemoTools,
		detail: pick(state.DemoTools,
			"available, with its tool server running",
			"hidden - this instance was started without --demo-tools"),
		fix: "start the service with --demo-tools --demo-dir <a directory the service user can write>",
	})

	add(preflightCheck{
		name: "Tool server", ok: strings.TrimSpace(a.MCPUpstream) != "",
		detail: firstNonBlank(a.MCPUpstream, "no upstream, so a gated tool call reaches nothing"),
		fix:    "set --mcp-upstream-url, or use --demo-tools which runs one in-process",
	})

	add(preflightCheck{
		name: "Policy", ok: a.PolicyLoaded,
		detail: pick(a.PolicyLoaded,
			fmt.Sprintf("loaded, %d tools approved", a.ApprovedTools),
			"built-in defaults - not the policy you wrote, and identical from outside"),
		fix: "start with --policy <file> and check that the file parses",
	})

	add(preflightCheck{
		name: "Audit chain", ok: a.AuditChainValid,
		detail: pick(a.AuditChainValid,
			fmt.Sprintf("intact through record %d", a.AuditChainIndex),
			"does not verify - a record was changed, removed, or pruned by retention"),
		fix: "investigate before demonstrating anything: this is the claim being sold",
	})

	// The check that decides whether the strongest sentence in the pitch can be
	// said at all. Where it is missing the console says so plainly, on screen,
	// in front of whoever is watching.
	witnessDetail := "not configured - the console will say this chain detects accidental corruption only"
	if a.WitnessConfigured && a.WitnessDiverged {
		witnessDetail = "DIVERGED - the witness holds a chain this server cannot produce"
	} else if a.WitnessConfigured {
		witnessDetail = fmt.Sprintf("agrees at record %d", a.WitnessIndex)
	}
	add(preflightCheck{
		name: "External witness", ok: a.WitnessConfigured && !a.WitnessDiverged, detail: witnessDetail,
		fix: "set --audit-witness-url and --audit-witness-token; without a witness the\n    operator-cannot-quietly-edit-this claim cannot be made at all",
	})

	add(preflightCheck{
		name: "Session marks", ok: a.SessionMarksDurable && a.SessionMarkError == "",
		detail: firstNonBlank(a.SessionMarkError, pick(a.SessionMarksDurable,
			"durable, surviving a restart",
			"in memory only - a restart releases every marked session at once")),
		fix: "run against Postgres; in-memory storage cannot keep marks across a restart",
	})

	add(preflightCheck{
		name: "Storage", ok: !a.DegradedMode,
		detail: pick(!a.DegradedMode,
			"deciding and persisting",
			"degraded - still deciding, but not persisting"),
		fix: "check the database before demonstrating anything about the record",
	})

	// Brokering is reported rather than required. A deployment that has not
	// adopted it is not broken, it just cannot make the dead-end claim, and the
	// difference is worth knowing before somebody asks.
	if credBody, credStatus, credErr := tenantCall(http.MethodGet, base+"/api/credentials", token, nil); credErr == nil && credStatus < 300 {
		var creds struct {
			Credentials []struct {
				Tool string `json:"tool"`
			} `json:"credentials"`
			StaticUpstreamToken bool `json:"static_upstream_token"`
		}
		if json.Unmarshal(credBody, &creds) == nil {
			count := len(creds.Credentials)
			detail := fmt.Sprintf("%d credential(s) held by the gateway", count)
			if count > 0 && creds.StaticUpstreamToken {
				detail += ", with a static token still set as the fallback"
			}
			if count == 0 {
				detail = "none - the agent holds its own tool credentials, so a route around the gateway still works"
			}
			add(preflightCheck{
				name: "Credential brokering", ok: count > 0, detail: detail,
				fix: "promtactl credential set --tool <name>, then remove that secret from the agent",
			})
		}
	}

	// The check that would have replaced most of the others.
	//
	// Everything above asks whether a component is configured. This asks
	// whether the demonstration actually completes - and it is the question
	// that went unasked while the demonstration was broken in production for
	// an unknown length of time, on a deployment where every other indicator
	// was green. "Available" and "works" are different claims.
	demoRan := false
	if state.DemoTools && !*skipDemo {
		add(runDemonstrationCheck(base, token))
		demoRan = true
	}

	// The approval count below came from the status read at the start, before
	// the demonstration ran. Reporting it unchanged would say "queue empty"
	// immediately after putting a call in the queue - a number that is correct
	// about a moment that has passed, which is the least useful kind of wrong.
	if demoRan {
		if fresh, freshStatus, freshErr := tenantCall(http.MethodGet, base+"/api/gateway/queue", token, nil); freshErr == nil && freshStatus < 300 {
			var queue struct {
				PendingActions []struct {
					ID string `json:"id"`
				} `json:"pending_actions"`
			}
			if json.Unmarshal(fresh, &queue) == nil {
				a.ApprovalsWaiting = len(queue.PendingActions)
			}
		}
	}

	// Not a fault, but the thing most likely to confuse an audience: a queue
	// left over from an earlier run makes the guarded run look like it leaked.
	queueDetail := "empty"
	if a.ApprovalsWaiting > 0 {
		queueDetail = fmt.Sprintf("%d call(s) waiting", a.ApprovalsWaiting)
		if demoRan {
			queueDetail += ", including the one this check just created"
		}
	}
	add(preflightCheck{
		name: "Approval queue", ok: a.ApprovalsWaiting == 0, detail: queueDetail,
		fix: "promtactl gateway decline --id <action> --reason ..., or leave them - but know\n" +
			"    they are there before the Approvals page is opened in front of anyone",
	})

	failed := 0
	for _, c := range checks {
		mark := "ok "
		if !c.ok {
			mark = "NOT"
			failed++
		}
		fmt.Printf("  [%s]  %-20s %s\n", mark, c.name, c.detail)
	}
	fmt.Println()

	if failed == 0 {
		fmt.Println("Nothing is missing. The demonstration runs end to end and the console can")
		fmt.Println("make its strongest claim without qualifying it.")
		return nil
	}

	fmt.Printf("%d of %d not ready:\n\n", failed, len(checks))
	for _, c := range checks {
		if !c.ok {
			fmt.Printf("  %s\n    %s\n\n", c.name, c.fix)
		}
	}
	return fmt.Errorf("not ready")
}

// runDemonstrationCheck executes the guarded demonstration and asserts what it
// is supposed to prove, rather than that it returned something.
//
// Three things have to hold, and each of them failed silently at some point:
// the run has to complete at all, its first call has to be allowed, and the
// outward action at the end has to be stopped. A run that completes with
// everything allowed proves nothing, and a run where the very first call is
// held looks identical to a broken product.
func runDemonstrationCheck(base string, token string) preflightCheck {
	body, status, err := tenantCall(http.MethodPost, base+"/api/demo/agent-run", token,
		map[string]string{"via": "gateway"})
	if err != nil {
		return preflightCheck{name: "Demonstration runs", detail: "could not be started: " + err.Error(),
			fix: "check the service log; the demonstration is the centrepiece of the pitch"}
	}
	if status >= 300 {
		detail := strings.TrimSpace(string(body))
		if len(detail) > 160 {
			detail = detail[:160] + "…"
		}
		return preflightCheck{name: "Demonstration runs", detail: "FAILED - " + detail,
			fix: "this is what an audience would see; do not present until it completes"}
	}

	var result struct {
		Steps []struct {
			Tool    string `json:"tool"`
			Outcome string `json:"outcome"`
		} `json:"steps"`
		Sent   bool `json:"sent"`
		Outbox int  `json:"outbox"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return preflightCheck{name: "Demonstration runs", detail: "returned something unreadable",
			fix: "check the service log"}
	}
	if len(result.Steps) == 0 {
		return preflightCheck{name: "Demonstration runs", detail: "completed without executing any step",
			fix: "the run reported success but did nothing; check the tool server"}
	}
	if result.Steps[0].Outcome != "allowed" {
		return preflightCheck{name: "Demonstration runs",
			detail: "first call was " + result.Steps[0].Outcome + ", not allowed",
			fix:    "an audience sees the product blocking its own demonstration; check agent identity and approved tools"}
	}

	stopped := 0
	for _, step := range result.Steps {
		if step.Outcome == "held" || step.Outcome == "withheld" {
			stopped++
		}
	}
	if stopped == 0 || result.Sent || result.Outbox > 0 {
		return preflightCheck{name: "Demonstration runs",
			detail: fmt.Sprintf("completed but nothing was stopped (held/withheld=%d, sent=%v, outbox=%d)",
				stopped, result.Sent, result.Outbox),
			fix: "the guarded run is supposed to stop the outward action; with nothing stopped it demonstrates the opposite"}
	}

	return preflightCheck{name: "Demonstration runs", ok: true,
		detail: fmt.Sprintf("%d steps, %d stopped, nothing left the host", len(result.Steps), stopped),
		fix:    ""}
}

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
