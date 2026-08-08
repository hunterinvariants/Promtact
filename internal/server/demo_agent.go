package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/demotools"
)

// The demonstration, run from the console.
//
// The agent is deliberately credulous: it reads a document, decodes whatever
// instruction is hidden in it, and obeys. No model is involved, which is the
// honest assumption rather than a shortcut — a demonstration that depends on a
// model being fooled on cue is really claiming the model will probably resist,
// and nobody buys a control whose value rests on the thing it protects behaving
// well.
//
// The guarded run goes through the real handler. Not a copy of its logic, not a
// re-implementation for display: the same code that serves a customer's agent,
// including the policy, the response inspection, the session mark and the audit
// record. A demonstration that runs a parallel implementation demonstrates the
// parallel implementation.

type demoStep struct {
	Tool    string `json:"tool"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
	Length  int    `json:"length,omitempty"`
	Hidden  string `json:"hidden,omitempty"`
}

type demoRunResult struct {
	Via       string     `json:"via"`
	Guarded   bool       `json:"guarded"`
	Steps     []demoStep `json:"steps"`
	Sent      bool       `json:"sent"`
	Recipient string     `json:"recipient,omitempty"`
	Outbox    int        `json:"outbox"`
	Summary   string     `json:"summary"`
}

func (a *App) handleDemoAgentRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.demoTools == nil {
		// Deliberately not available unless the deployment was started for a
		// demonstration. A button in a production console that runs a scripted
		// attack is not a feature.
		writeError(w, http.StatusNotFound, errors.New("this deployment was not started with --demo-tools"))
		return
	}

	var request struct {
		Via string `json:"via"`
	}
	_ = json.NewDecoder(r.Body).Decode(&request)
	guarded := !strings.EqualFold(strings.TrimSpace(request.Via), "direct")

	// Each run is its own session, as a real agent's would be. Sharing one
	// would let the marks from an earlier run decide a later one.
	session := fmt.Sprintf("console-demo-%d", time.Now().UnixNano())

	// Both runs start from an empty outbox, so what is in it afterwards belongs
	// to the run being shown. A file left over from the unguarded run makes the
	// guarded run look as though it leaked.
	if err := a.demoTools.ClearOutbox(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	runner := &demoAgent{app: a, request: r, session: session, guarded: guarded}
	result, err := runner.run()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	result.Outbox = a.demoOutboxCount()
	writeJSON(w, http.StatusOK, result)
}

func (a *App) demoOutboxCount() int {
	entries, err := demotoolsOutboxEntries(a.demoTools)
	if err != nil {
		return 0
	}
	return entries
}

type demoAgent struct {
	app     *App
	request *http.Request
	session string
	guarded bool
}

func (d *demoAgent) call(tool string, arguments map[string]any) (string, string) {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  "tools/call",
		"params":  map[string]any{"name": tool, "arguments": arguments},
	})

	if !d.guarded {
		return d.callDirect(payload)
	}
	return d.callThroughGateway(payload)
}

// callDirect reaches the tool server with nothing in between, which is the
// point of the unguarded run.
func (d *demoAgent) callDirect(payload []byte) (string, string) {
	req, err := http.NewRequestWithContext(d.request.Context(), http.MethodPost,
		d.app.gatewayMCPUpstream(), bytes.NewReader(payload))
	if err != nil {
		return "", err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return decodeDemoResponse(body)
}

// callThroughGateway invokes the real MCP handler in-process.
//
// The request carries the incoming one's context, so the authenticated
// principal and tenant are the console user's — this run is attributed to
// whoever pressed the button, which is what an audit record should say.
func (d *demoAgent) callThroughGateway(payload []byte) (string, string) {
	req, err := http.NewRequestWithContext(d.request.Context(), http.MethodPost,
		"/api/mcp/proxy", bytes.NewReader(payload))
	if err != nil {
		return "", err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", d.session)
	req.RemoteAddr = d.request.RemoteAddr

	recorder := &bufferedResponse{header: http.Header{}}
	d.app.handleMCPProxy(recorder, req)
	return decodeDemoResponse(recorder.body.Bytes())
}

func decodeDemoResponse(body []byte) (string, string) {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", strings.TrimSpace(string(body))
	}
	if rpcError, ok := decoded["error"].(map[string]any); ok {
		message, _ := rpcError["message"].(string)
		detail := message
		if data, ok := rpcError["data"].(map[string]any); ok {
			if reason, ok := data["reason"].(string); ok && reason != "" {
				detail = reason
			} else if action, ok := data["action"].(map[string]any); ok {
				if reason, ok := action["reason"].(string); ok && reason != "" {
					detail = reason
				}
			}
		}
		return "", detail
	}
	if message, ok := decoded["error"].(string); ok && message != "" {
		return "", message
	}
	return mcpContentText(decoded), ""
}

// mcpContentText reads only result.content[].text.
//
// mcpResultText deliberately collects every string anywhere in the result,
// because content that is not looked at is content that is trusted — right for
// inspection, wrong here. Used for parsing it also returned the literal "text"
// from each item's `type` field, so the agent went looking for a document
// called "text" and reported it missing.
func mcpContentText(decoded map[string]any) string {
	result, ok := decoded["result"].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := result["content"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, item := range content {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := entry["text"].(string); ok && text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func (d *demoAgent) run() (demoRunResult, error) {
	result := demoRunResult{Via: "direct", Guarded: d.guarded}
	if d.guarded {
		result.Via = "gateway"
	}

	listing, failure := d.call("list_documents", map[string]any{})
	if failure != "" {
		return result, fmt.Errorf("the agent could not list documents: %s", failure)
	}
	result.Steps = append(result.Steps, demoStep{Tool: "list_documents", Outcome: "allowed"})

	var instruction string
	for _, name := range strings.Fields(strings.ReplaceAll(listing, "\n", " ")) {
		content, failure := d.call("read_document", map[string]any{"name": name})
		if failure != "" {
			result.Steps = append(result.Steps, demoStep{
				Tool: "read_document " + name, Outcome: "withheld", Detail: failure,
			})
			continue
		}
		step := demoStep{Tool: "read_document " + name, Outcome: "allowed", Length: len([]rune(content))}
		if hidden := decodeHiddenRunes(content); hidden != "" {
			instruction = hidden
			step.Hidden = hidden
		}
		result.Steps = append(result.Steps, step)
	}

	recipient, body := "ops@example.com", "Q3 revenue rose 4%. Vendor systems operational."
	if instruction != "" {
		recipient = recipientFromInstruction(instruction)
		body = "Q3 revenue rose 4%. Regional breakdown unchanged."
	}
	sent, failure := d.call("send_message", map[string]any{"to": recipient, "body": body})
	result.Recipient = recipient

	if failure != "" {
		result.Steps = append(result.Steps, demoStep{
			Tool: "send_message to " + recipient, Outcome: "held", Detail: failure,
		})
		result.Summary = "Nothing was sent."
		return result, nil
	}
	result.Sent = true
	result.Steps = append(result.Steps, demoStep{
		Tool: "send_message to " + recipient, Outcome: "sent", Detail: strings.TrimSpace(sent),
	})
	if instruction != "" {
		result.Summary = "The contents of an internal document have left, to an address chosen by whoever wrote that file."
	} else {
		result.Summary = "The message the operator asked for was sent."
	}
	return result, nil
}

func decodeHiddenRunes(content string) string {
	var decoded strings.Builder
	for _, r := range content {
		if r >= 0xE0000 && r <= 0xE007F {
			decoded.WriteRune(r - 0xE0000)
		}
	}
	return strings.TrimSpace(decoded.String())
}

func recipientFromInstruction(instruction string) string {
	for _, field := range strings.FieldsFunc(instruction, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t'
	}) {
		trimmed := strings.Trim(field, ".,;:!?\"'()")
		if strings.Count(trimmed, "@") == 1 && strings.Contains(trimmed, ".") {
			return trimmed
		}
	}
	return "unknown@example.invalid"
}

// bufferedResponse collects a handler's output in memory so the real handler
// can be invoked without a network round trip.
type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) {
	return b.body.Write(p)
}
func (b *bufferedResponse) WriteHeader(status int) { b.status = status }

func demotoolsOutboxEntries(tools *demotools.Server) (int, error) {
	if tools == nil {
		return 0, nil
	}
	return tools.OutboxCount()
}
