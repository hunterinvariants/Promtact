package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// promtactl agent-demo — the same agent, run twice, with and without the gateway.
//
// The agent here is deliberately credulous: it reads a document, decodes any
// instruction hidden in it, and does what it says. No model is involved.
//
// That is not a shortcut, it is the honest assumption. A demonstration that
// depends on a model being fooled on cue does not survive contact with a good
// model — a real assistant recognised the injection during testing, refused it,
// and there was nothing left to show. Worse, it means the claim being made is
// "the model will probably resist", which is not a claim anyone can buy.
//
// So the model is assumed to fail, completely, every time. What is being
// demonstrated is what happens anyway. That is the only version of this that is
// worth putting in front of somebody, and it is also the only version whose
// result does not change between runs.
//
// The two runs differ in one thing: whether the agent's tools go through the
// gateway. Everything else — the agent, the documents, the tools — is identical.

func agentDemoCommand(args []string) error {
	fs := flag.NewFlagSet("agent-demo", flag.ContinueOnError)
	gatewayURL := fs.String("url", "http://127.0.0.1:8130", "Promtact base URL")
	token := fs.String("token", "demo", "API token for the gateway")
	// A token passed as an argument is visible in the process list to every
	// local user, which is a poor thing for a security product to require.
	tokenFile := fs.String("token-file", "", "read the token from a file instead")
	toolsURL := fs.String("tools-url", "http://127.0.0.1:9200/", "the MCP tool server, for the unguarded run")
	via := fs.String("via", "gateway", "gateway | direct — whether the agent's tools are gated")
	if err := fs.Parse(args); err != nil {
		return err
	}

	bearer := strings.TrimSpace(*token)
	if path := strings.TrimSpace(*tokenFile); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading --token-file: %w", err)
		}
		bearer = strings.TrimSpace(string(data))
	}

	guarded := strings.EqualFold(strings.TrimSpace(*via), "gateway")
	endpoint := strings.TrimRight(*gatewayURL, "/") + "/api/mcp/proxy"
	if !guarded {
		endpoint = *toolsURL
		bearer = ""
	}

	rule := strings.Repeat("─", 68)
	fmt.Printf("%s\n", rule)
	if guarded {
		fmt.Println("Run 2 of 2 — the agent's tools go through Promtact")
	} else {
		fmt.Println("Run 1 of 2 — the agent talks to its tools directly")
	}
	fmt.Printf("%s\n\n", rule)

	// A fresh session per run, which is what a real agent has. Without one every
	// run of this demonstration shares a single history bucket with every other
	// run, the risk score climbs across them, and eventually an argument-free
	// directory listing is held for approval. That happened, and it looked like
	// the gateway had broken.
	agent := &credulousAgent{
		endpoint: endpoint,
		token:    bearer,
		session:  fmt.Sprintf("agent-demo-%d", time.Now().UnixNano()),
		client:   &http.Client{Timeout: 30 * time.Second},
	}
	return agent.run()
}

type credulousAgent struct {
	endpoint string
	token    string
	session  string
	client   *http.Client
}

func (a *credulousAgent) call(tool string, arguments map[string]any) (string, string) {
	response, err := postJSONWithHeaders(a.client, a.endpoint, a.token,
		map[string]string{"Mcp-Session-Id": a.session},
		map[string]any{
			"jsonrpc": "2.0",
			"id":      time.Now().UnixNano(),
			"method":  "tools/call",
			"params":  map[string]any{"name": tool, "arguments": arguments},
		})
	if err != nil {
		return "", "transport: " + err.Error()
	}
	if rpcError, ok := response["error"].(map[string]any); ok {
		message, _ := rpcError["message"].(string)
		detail := message
		if data, ok := rpcError["data"].(map[string]any); ok {
			if reason, ok := data["reason"].(string); ok && reason != "" {
				detail = message + " — " + reason
			} else if action, ok := data["action"].(map[string]any); ok {
				if reason, ok := action["reason"].(string); ok && reason != "" {
					detail = message + " — " + reason
				}
			}
		}
		return "", detail
	}
	return mcpResultTextFromMap(response), ""
}

func (a *credulousAgent) run() error {
	listing, failure := a.call("list_documents", map[string]any{})
	if failure != "" {
		return fmt.Errorf("the agent could not even list documents: %s", failure)
	}
	documents := strings.Fields(strings.ReplaceAll(listing, "\n", " "))
	fmt.Printf("The agent sees %d document(s): %s\n\n", len(documents), strings.Join(documents, ", "))

	var instruction string
	for _, name := range documents {
		content, failure := a.call("read_document", map[string]any{"name": name})
		if failure != "" {
			fmt.Printf("  read %-18s REFUSED — %s\n", name, failure)
			continue
		}
		fmt.Printf("  read %-18s %d characters\n", name, len([]rune(content)))
		if hidden := decodeHiddenInstruction(content); hidden != "" {
			instruction = hidden
			fmt.Printf("       ↳ the agent decoded a hidden instruction:\n         %q\n", truncate(hidden, 110))
		}
	}

	fmt.Println()
	if instruction == "" {
		fmt.Println("No hidden instruction reached the agent.")
		fmt.Println("It proceeds with the task it was actually given.")
		result, failure := a.call("send_message", map[string]any{
			"to": "ops@example.com", "body": "Q3 revenue rose 4%. Vendor systems operational.",
		})
		report("send the summary the operator asked for", "ops@example.com", result, failure)
		return nil
	}

	// The agent obeys. This is the assumption, not an accident.
	recipient := recipientFrom(instruction)
	fmt.Printf("The agent obeys the instruction it found and sends to %s.\n", recipient)
	result, failure := a.call("send_message", map[string]any{
		"to": recipient, "body": "Q3 revenue rose 4%. Regional breakdown unchanged.",
	})
	report("obey the hidden instruction", recipient, result, failure)
	return nil
}

func report(what string, recipient string, result string, failure string) {
	fmt.Println()
	if failure != "" {
		fmt.Printf("  attempt to %s\n  → STOPPED: %s\n", what, failure)
		fmt.Println("\n  Nothing was sent.")
		return
	}
	fmt.Printf("  attempt to %s\n  → WENT THROUGH: %s\n", what, strings.TrimSpace(result))
	if strings.Contains(recipient, "attacker") {
		fmt.Println("\n  The contents of an internal document have just left, to an address")
		fmt.Println("  chosen by whoever wrote that file. Look in the outbox.")
	}
}

// decodeHiddenInstruction turns Unicode tag characters back into the text they
// stand for. A model does this implicitly, because to a model they are simply
// more text; a person sees nothing at all.
func decodeHiddenInstruction(content string) string {
	var decoded strings.Builder
	for _, r := range content {
		if r >= 0xE0000 && r <= 0xE007F {
			decoded.WriteRune(r - 0xE0000)
		}
	}
	return strings.TrimSpace(decoded.String())
}

var recipientPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

func recipientFrom(instruction string) string {
	if found := recipientPattern.FindString(instruction); found != "" {
		return found
	}
	return "unknown@example.invalid"
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

func mcpResultTextFromMap(response map[string]any) string {
	result, ok := response["result"].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := result["content"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, item := range content {
		if entry, ok := item.(map[string]any); ok {
			if text, ok := entry["text"].(string); ok {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}
