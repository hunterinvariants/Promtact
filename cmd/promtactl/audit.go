package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// promtactl audit — the record, in the form somebody would actually read it.
//
// The enforcement half of this product demonstrates well: something is stopped,
// and the outbox is empty. The evidence half does not demonstrate at all unless
// there is a way to show it, and a JSON dump from the API is not that.
//
// The output is deliberately explicit about what is *not* here. Promtact sits
// between an agent and its tools, so it records what the agent asked a tool to
// do. It does not see the prompt, the conversation, or the model's reasoning,
// and a claim that it does will be caught in the first serious room it is made
// in.

func auditCommand(args []string) error {
	if len(args) == 0 {
		auditUsage()
		return fmt.Errorf("audit requires a subcommand")
	}
	switch args[0] {
	case "trail":
		return auditTrail(args[1:])
	case "verify":
		return auditVerify(args[1:])
	default:
		auditUsage()
		return fmt.Errorf("unknown audit subcommand %q", args[0])
	}
}

func auditUsage() {
	fmt.Fprintln(os.Stderr, "usage: promtactl audit trail [--last 10]")
	fmt.Fprintln(os.Stderr, "       promtactl audit verify")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "trail  — what the gateway decided, in order, in plain language")
	fmt.Fprintln(os.Stderr, "verify — whether the chain is intact and what the external witness holds")
}

type auditRecord struct {
	ID         string            `json:"id"`
	Action     string            `json:"action"`
	Outcome    string            `json:"outcome"`
	Actor      string            `json:"actor"`
	Timestamp  time.Time         `json:"timestamp"`
	ChainIndex int               `json:"chain_index"`
	Metadata   map[string]string `json:"metadata"`
}

func auditTrail(args []string) error {
	fs := flag.NewFlagSet("audit trail", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	last := fs.Int("last", 10, "how many records to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	body, status, err := tenantCall(http.MethodGet, base+"/api/audit", token, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("reading the audit trail failed (%d): %s", status, strings.TrimSpace(string(body)))
	}
	var records []auditRecord
	if err := json.Unmarshal(body, &records); err != nil {
		return fmt.Errorf("unexpected response: %s", body)
	}
	if len(records) == 0 {
		fmt.Println("No audit records.")
		return nil
	}
	// The API returns newest first, which is right for a feed and wrong for a
	// sequence of events somebody is being walked through.
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
	if *last > 0 && len(records) > *last {
		records = records[len(records)-*last:]
	}

	fmt.Printf("%s\nWhat the gateway decided, oldest first\n%s\n", strings.Repeat("─", 68), strings.Repeat("─", 68))

	for _, record := range records {
		meta := record.Metadata
		if meta == nil {
			meta = map[string]string{}
		}
		fmt.Printf("\n%s  #%d  %s\n", record.Timestamp.Local().Format("15:04:05"), record.ChainIndex, describeOutcome(record, meta))

		if tool := meta["tool"]; tool != "" {
			fmt.Printf("           tool      %s\n", tool)
		}
		if actor := strings.TrimSpace(record.Actor); actor != "" {
			fmt.Printf("           by        %s\n", actor)
		}
		if reason := meta["result_reason"]; reason != "" {
			fmt.Printf("           because   %s\n", reason)
		} else if reason := meta["reason"]; reason != "" {
			fmt.Printf("           because   %s\n", reason)
		}
		if record.ID != "" {
			fmt.Printf("           record    %s\n", record.ID)
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("─", 68))
	fmt.Println("Each record is hash-linked to the one before it. Run")
	fmt.Println("`promtactl audit verify` to check the chain and the external witness.")
	fmt.Println()
	fmt.Println("What is NOT in here: the prompt, the conversation, and the model's")
	fmt.Println("reasoning. This gateway sits between an agent and its tools, so it")
	fmt.Println("records what the agent asked a tool to do and what was decided.")
	return nil
}

func describeOutcome(record auditRecord, meta map[string]string) string {
	switch record.Outcome {
	case "withheld":
		return "WITHHELD — a tool's answer was kept from the agent"
	case "pending_approval":
		return "HELD — waiting for a person"
	case "blocked":
		return "DENIED — the call did not run"
	case "executed":
		return "allowed"
	case "removed":
		return "REMOVED — an asset and its records were deleted"
	case "failed":
		return "FAILED — " + meta["error"]
	default:
		return record.Outcome
	}
}

func auditVerify(args []string) error {
	fs := flag.NewFlagSet("audit verify", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	body, status, err := tenantCall(http.MethodGet, base+"/api/audit/chain", token, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("reading the chain failed (%d): %s", status, strings.TrimSpace(string(body)))
	}
	var chain struct {
		Total    int    `json:"total"`
		Linked   int    `json:"linked"`
		Unlinked int    `json:"unlinked"`
		Head     string `json:"head"`
		Valid    bool   `json:"valid"`
		Anchor   string `json:"anchor"`
		Anchored bool   `json:"anchored"`
	}
	if err := json.Unmarshal(body, &chain); err != nil {
		return fmt.Errorf("unexpected response: %s", body)
	}

	fmt.Printf("Records      %d\n", chain.Total)
	fmt.Printf("Hash-linked  %d", chain.Linked)
	if chain.Unlinked > 0 {
		fmt.Printf("  (%d NOT linked)", chain.Unlinked)
	}
	fmt.Println()
	if chain.Valid {
		fmt.Println("Chain        intact — every record still hashes to the one before it")
	} else {
		fmt.Println("Chain        BROKEN — a record has been changed or removed")
	}
	fmt.Printf("Head         %s\n", chain.Head)

	fmt.Println()
	if chain.Anchored {
		fmt.Printf("Witness      published, holding %s\n", truncate(chain.Anchor, 32))
		// This is the part that matters and the part that is easy to overstate.
		// The chain alone proves nothing against whoever runs the server: they
		// could rewrite every record and recompute every hash. The witness is
		// what makes that visible, because it already holds an earlier head and
		// refuses a chain that got shorter or changed underneath it.
		fmt.Println("             The witness refuses a chain that has been shortened or")
		fmt.Println("             rewritten, so an operator cannot quietly edit this record")
		fmt.Println("             even with full access to the server and its database.")
	} else {
		fmt.Println("Witness      NOT configured")
		fmt.Println("             Without it the chain only detects accidental corruption:")
		fmt.Println("             anyone who can write to the database can rewrite every")
		fmt.Println("             record and recompute every hash. Say so rather than")
		fmt.Println("             claiming tamper-proof.")
	}
	return nil
}
