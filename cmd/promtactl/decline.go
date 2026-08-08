package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
)

// promtactl gateway queue / decline - see what is held, and say no to it.
//
// Until now the only way to empty the approval queue was to approve, and
// approving executes. So a held call was either eventually performed or left
// waiting forever, and there was no way to record that a person looked at it
// and decided against it.

func gatewayQueueCommand(args []string) error {
	fs := flag.NewFlagSet("gateway queue", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	body, status, err := tenantCall(http.MethodGet, base+"/api/gateway/queue", token, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("reading the queue returned %d: %s", status, strings.TrimSpace(string(body)))
	}
	var queue struct {
		PendingActions []struct {
			ID        string            `json:"id"`
			Target    string            `json:"target"`
			Reason    string            `json:"reason"`
			CreatedAt string            `json:"created_at"`
			Metadata  map[string]string `json:"metadata"`
		} `json:"pending_actions"`
	}
	if err := json.Unmarshal(body, &queue); err != nil {
		return fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(body)))
	}

	if len(queue.PendingActions) == 0 {
		fmt.Println("Nothing is waiting for a person.")
		return nil
	}
	fmt.Printf("%d call(s) held for a person:\n\n", len(queue.PendingActions))
	for _, action := range queue.PendingActions {
		fmt.Printf("  %s\n", action.ID)
		fmt.Printf("    tool     %s\n", firstNonBlank(action.Target, action.Metadata["tool"]))
		fmt.Printf("    because  %s\n", action.Reason)
		fmt.Printf("    held     %s\n\n", action.CreatedAt)
	}
	fmt.Println("Approve one with the console, or refuse it:")
	fmt.Println("  promtactl gateway decline --id <id> --reason \"...\"")
	return nil
}

func gatewayDeclineCommand(args []string) error {
	fs := flag.NewFlagSet("gateway decline", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	id := fs.String("id", "", "action id to refuse")
	reason := fs.String("reason", "", "why it was refused; this goes into the audit chain")
	all := fs.Bool("all", false, "refuse every held call (use before a demonstration, not in production)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}
	if strings.TrimSpace(*reason) == "" {
		// The reason is the point. A refusal with no reason recorded answers
		// "was this allowed" but not "why not", which is the question anyone
		// reads an audit trail to answer.
		return fmt.Errorf("--reason is required: it is recorded in the audit chain and is the\n" +
			"  half of the decision that a bare 'declined' cannot express")
	}

	ids := []string{strings.TrimSpace(*id)}
	if *all {
		body, status, err := tenantCall(http.MethodGet, base+"/api/gateway/queue", token, nil)
		if err != nil {
			return err
		}
		if status >= 300 {
			return fmt.Errorf("reading the queue returned %d: %s", status, strings.TrimSpace(string(body)))
		}
		var queue struct {
			PendingActions []struct {
				ID string `json:"id"`
			} `json:"pending_actions"`
		}
		if err := json.Unmarshal(body, &queue); err != nil {
			return fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(body)))
		}
		ids = ids[:0]
		for _, action := range queue.PendingActions {
			ids = append(ids, action.ID)
		}
		if len(ids) == 0 {
			fmt.Println("Nothing is waiting for a person.")
			return nil
		}
	}
	if len(ids) == 1 && ids[0] == "" {
		return fmt.Errorf("--id is required, or --all to refuse every held call")
	}

	for _, actionID := range ids {
		body, status, err := tenantCall(http.MethodPost, base+"/api/responses/decline", token,
			map[string]string{"action_id": actionID, "reason": *reason})
		if err != nil {
			return err
		}
		if status >= 300 {
			return fmt.Errorf("refusing %s returned %d: %s", actionID, status, strings.TrimSpace(string(body)))
		}
		fmt.Printf("Refused %s - it will not run, and the refusal is in the audit chain.\n", actionID)
	}
	return nil
}
