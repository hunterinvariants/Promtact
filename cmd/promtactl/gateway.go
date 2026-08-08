package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// promtactl gateway — the part of the product that acts rather than reports.
//
// Every other surface here describes something that already happened. This one
// stands in front of the call: an agent asks to use a tool, the gateway returns
// allow, require approval, or deny, and on a deny the tool never runs. That is
// the difference between this and an endpoint product, and until now there was
// no way to see it happen without writing an HTTP client by hand.
//
// `call` is what an integrator wires an agent into. `demo` plays four calls
// through the same path and says what each one proves.

func gatewayCommand(args []string) error {
	if len(args) == 0 {
		gatewayUsage()
		return fmt.Errorf("gateway requires a subcommand")
	}
	switch args[0] {
	case "call":
		return gatewayCall(args[1:])
	case "demo":
		return gatewayDemo(args[1:])
	case "queue":
		return gatewayQueueCommand(args[1:])
	case "decline":
		return gatewayDeclineCommand(args[1:])
	case "chain-demo":
		return gatewayChainDemo(args[1:])
	default:
		gatewayUsage()
		return fmt.Errorf("unknown gateway subcommand %q", args[0])
	}
}

func gatewayUsage() {
	fmt.Fprintln(os.Stderr, "usage: promtactl gateway call --tool <name> [--command ...] [--arguments ...] [--destination ...]")
	fmt.Fprintln(os.Stderr, "       promtactl gateway demo         four calls, each verdict explained")
	fmt.Fprintln(os.Stderr, "       promtactl gateway chain-demo   the two-step injection chain, end to end")
	fmt.Fprintln(os.Stderr, "                                      (runs on the gateway host; serves its own page)")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Submits a tool call for a verdict. A denied call does not run.")
	fmt.Fprintln(os.Stderr, "Credentials: --token, --token-file, or PROMTACT_API_TOKEN.")
	fmt.Fprintln(os.Stderr, "Agent identity: --agent-id and --agent-token. Without one the gateway")
	fmt.Fprintln(os.Stderr, "holds the call for a person, however ordinary the tool.")
}

type gatewayClientFlags struct {
	baseURL    *string
	token      *string
	file       *string
	actor      *string
	asset      *string
	agentID    *string
	agentToken *string
}

func gatewayClientCommonFlags(fs *flag.FlagSet) gatewayClientFlags {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown-host"
	}
	return gatewayClientFlags{
		baseURL: fs.String("url", firstNonEmpty(os.Getenv("PROMTACT_URL"), "http://127.0.0.1:8080"), "Promtact base URL"),
		token:   fs.String("token", os.Getenv("PROMTACT_API_TOKEN"), "API token"),
		file:    fs.String("token-file", "", "read the token from a file instead"),
		actor:   fs.String("actor", "promtactl", "the agent or account making the call"),
		asset:   fs.String("asset", host, "the machine the call originates from"),
		// An agent that does not identify itself gets nothing unattended: the
		// gateway holds its calls for a person. Registering the identity in the
		// policy is what lets an agent run without one.
		agentID:    fs.String("agent-id", os.Getenv("PROMTACT_AGENT_ID"), "registered agent identity making the call"),
		agentToken: fs.String("agent-token", os.Getenv("PROMTACT_AGENT_SECRET"), "secret proving that identity"),
	}
}

// applyIdentity is separate from the call itself so both subcommands carry the
// identity the same way, and so an unset identity stays visibly unset rather
// than quietly becoming an empty string somewhere deeper.
func (f gatewayClientFlags) applyIdentity(request *domain.ToolCallRequest) {
	request.AssetID = *f.asset
	request.Hostname = *f.asset
	request.Actor = *f.actor
	request.AgentID = strings.TrimSpace(*f.agentID)
	request.AgentToken = strings.TrimSpace(*f.agentToken)
}

func (f gatewayClientFlags) resolve() (string, string, error) {
	token := strings.TrimSpace(*f.token)
	if path := strings.TrimSpace(*f.file); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", "", fmt.Errorf("reading --token-file: %w", err)
		}
		token = strings.TrimSpace(string(data))
	}
	if token == "" {
		return "", "", fmt.Errorf("a token is required: pass --token, --token-file, or set PROMTACT_API_TOKEN")
	}
	return strings.TrimRight(strings.TrimSpace(*f.baseURL), "/"), token, nil
}

func gatewayCall(args []string) error {
	fs := flag.NewFlagSet("gateway call", flag.ContinueOnError)
	common := gatewayClientCommonFlags(fs)
	tool := fs.String("tool", "", "tool the agent wants to call (required)")
	command := fs.String("command", "", "what the tool is being asked to do")
	arguments := fs.String("arguments", "", "arguments passed to the tool")
	destination := fs.String("destination", "", "outbound destination the call would reach")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*tool) == "" {
		return fmt.Errorf("--tool is required")
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	request := domain.ToolCallRequest{
		ToolName:    strings.TrimSpace(*tool),
		Command:     *command,
		Arguments:   *arguments,
		Destination: *destination,
		Labels:      []string{"agent", "tool-call"},
	}
	common.applyIdentity(&request)

	client := &http.Client{Timeout: 20 * time.Second}
	result, err := postGatewayExecution(client, base, token, request)
	if err != nil {
		return err
	}
	printGatewayResult(result, base)
	return nil
}

// printGatewayResult states the verdict and its consequence in the same breath.
// "deny" alone invites the question of whether the tool ran anyway, which is the
// one thing a reader must not have to wonder about.
func printGatewayResult(result domain.ToolExecutionResult, base string) {
	decision := result.Decision
	fmt.Printf("Tool call  %s\n", firstNonEmpty(decision.ToolName, "(unnamed)"))

	switch result.Status {
	case "allow", "executed":
		fmt.Println("Verdict    ALLOWED — the call proceeded")
	case "blocked":
		fmt.Println("Verdict    DENIED — the call did not run")
	case "pending_approval":
		fmt.Println("Verdict    HELD — the call is waiting for a person to approve it")
		if result.Action != nil {
			fmt.Printf("           pending action %s\n", result.Action.ID)
		}
	default:
		fmt.Printf("Verdict    %s\n", result.Status)
	}

	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		fmt.Printf("Because    %s\n", reason)
	}
	if decision.Risk != "" {
		fmt.Printf("Risk       %s\n", decision.Risk)
	}

	if len(decision.Alerts) > 0 {
		fmt.Printf("\nRaised %d alert(s):\n", len(decision.Alerts))
		for _, alert := range decision.Alerts {
			fmt.Printf("  %-26s %s\n", alert.RuleID, alert.Title)
		}
	}

	// The audit record is the point of the exercise: the verdict is not just
	// returned to the caller, it is written into a chain that cannot be edited
	// afterwards without the witness noticing.
	if decision.RequestID != "" {
		fmt.Printf("\nRecorded as gateway.decide, request %s\n", decision.RequestID)
	}
	fmt.Printf("Console     %s\n", base)
}

func gatewayDemo(args []string) error {
	fs := flag.NewFlagSet("gateway demo", flag.ContinueOnError)
	common := gatewayClientCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	cases := []struct {
		heading string
		proves  string
		request domain.ToolCallRequest
	}{
		{
			heading: "An approved tool, called by an agent that has not identified itself",
			proves: "The tool is permitted and the work is ordinary. Whether it proceeds on " +
				"its own is a separate question, and the `Because` line above is the answer " +
				"for this deployment — an agent that cannot say who it is, or one whose recent " +
				"history is unusual, gets a person rather than a result. Register the agent " +
				"and pass --agent-id with --agent-token to let routine work through unattended.",
			request: domain.ToolCallRequest{
				ToolName: "asset_inventory",
				Command:  "list assets",
			},
		},
		{
			heading: "A tool no policy approves",
			proves:  "This is the one an endpoint product cannot do. The call is refused before the tool is reached — not detected afterwards, not alerted on while it runs.",
			request: domain.ToolCallRequest{
				ToolName: "remote_shell",
				Command:  "open a shell on the finance server",
			},
		},
		{
			heading: "An approved tool carrying a credential",
			proves:  "The tool is permitted; the content is not. Anything in an agent's context can reach a model prompt or a third party, and a credential that goes in cannot be taken back out.",
			request: domain.ToolCallRequest{
				ToolName:  "asset_inventory",
				Command:   "inspect inventory",
				Arguments: "api_key=sk-live-2f9d41c7b8e64a15",
			},
		},
		{
			heading: "An approved tool reaching an unapproved destination",
			proves:  "Data leaving is the step that cannot be undone, so the destination is gated separately from the tool.",
			request: domain.ToolCallRequest{
				ToolName:    "asset_inventory",
				Command:     "export inventory",
				Destination: "files.unknown-vendor.example",
			},
		},
	}

	client := &http.Client{Timeout: 20 * time.Second}
	fmt.Println("Four tool calls through the gateway. Each one is real: it is")
	fmt.Println("evaluated, recorded in the audit chain, and visible in the console.")

	if strings.TrimSpace(*common.agentID) == "" {
		fmt.Println("\nNo agent identity was given, so every call below is made by an agent")
		fmt.Println("the deployment cannot name. Where the policy registers agent identities,")
		fmt.Println("that alone is enough to hold a call for a person — so read the `Because`")
		fmt.Println("line on each verdict rather than assuming which control produced it.")
	}

	for i, tc := range cases {
		request := tc.request
		common.applyIdentity(&request)
		request.Labels = []string{"agent", "tool-call", "demo"}

		fmt.Printf("\n%s\n%d. %s\n%s\n\n", strings.Repeat("─", 68), i+1, tc.heading, strings.Repeat("─", 68))
		result, err := postGatewayExecution(client, base, token, request)
		if err != nil {
			return fmt.Errorf("%s: %w", tc.heading, err)
		}
		printGatewayResult(result, base)
		fmt.Printf("\nWhat this shows: %s\n", tc.proves)
	}

	fmt.Printf("\n%s\n", strings.Repeat("─", 68))
	fmt.Println("Held calls are waiting in the console under Responses; approving one")
	fmt.Println("there is what lets it run. Every verdict above is also an audit record,")
	fmt.Println("and the witness makes those records hard to remove quietly.")
	fmt.Println()
	fmt.Println("To let an agent work without a person on each call, register it in the")
	fmt.Println("policy under agent_identities with the SHA-256 of its secret:")
	fmt.Println("  promtactl token-hash --token <the agent's secret>")
	fmt.Println("then call with --agent-id and --agent-token.")
	return nil
}
