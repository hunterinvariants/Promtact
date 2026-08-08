package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// promtactl credential - manage the secrets the gateway presents upstream.
//
// The secret is read from an environment variable rather than a flag, because
// a flag is visible in the process list and in shell history, and a credential
// that leaks while being installed did not need any of the protection that
// follows.

func credentialCommand(args []string) error {
	if len(args) == 0 {
		credentialUsage()
		return fmt.Errorf("expected a subcommand")
	}
	switch args[0] {
	case "list":
		return credentialList(args[1:])
	case "set":
		return credentialSet(args[1:])
	case "remove":
		return credentialRemove(args[1:])
	default:
		credentialUsage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func credentialUsage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  promtactl credential list")
	fmt.Fprintln(os.Stderr, "  promtactl credential set --tool github_* [--header ...] [--scheme ...] [--description ...]")
	fmt.Fprintln(os.Stderr, "      the secret is read from PROMTACT_CREDENTIAL_SECRET, never from a flag")
	fmt.Fprintln(os.Stderr, "  promtactl credential remove --id cred-...")
}

func credentialList(args []string) error {
	fs := flag.NewFlagSet("credential list", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}
	body, status, err := tenantCall(http.MethodGet, base+"/api/credentials", token, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("listing credentials returned %d: %s", status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Credentials []struct {
			ID          string `json:"id"`
			Tool        string `json:"tool"`
			Header      string `json:"header"`
			Fingerprint string `json:"fingerprint"`
			Description string `json:"description"`
			LastUsedAt  string `json:"last_used_at"`
			UseCount    int64  `json:"use_count"`
		} `json:"credentials"`
		StaticUpstreamToken bool `json:"static_upstream_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(body)))
	}

	if len(payload.Credentials) == 0 {
		fmt.Println("No brokered credentials. Tool calls use the statically configured upstream token,")
		fmt.Println("which means the agent's own credential is whatever you gave it directly.")
	}
	for _, credential := range payload.Credentials {
		used := "never used"
		if credential.UseCount > 0 {
			used = fmt.Sprintf("used %d times, last %s", credential.UseCount, credential.LastUsedAt)
		}
		fmt.Printf("%-18s  %-24s  %s  %s\n", credential.ID, credential.Tool, credential.Fingerprint, used)
		if credential.Description != "" {
			fmt.Printf("%-18s  %s\n", "", credential.Description)
		}
	}
	if payload.StaticUpstreamToken && len(payload.Credentials) > 0 {
		fmt.Println()
		fmt.Println("A static upstream token is also configured. Tools with no credential above")
		fmt.Println("fall back to it, so those calls are not brokered.")
	}
	return nil
}

func credentialSet(args []string) error {
	fs := flag.NewFlagSet("credential set", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	tool := fs.String("tool", "", `tool pattern: an exact name, a "prefix_*" wildcard, or "*" for every tool`)
	header := fs.String("header", "", "header to present the secret in (default Authorization)")
	scheme := fs.String("scheme", "", "scheme inside that header (default Bearer for Authorization)")
	description := fs.String("description", "", "what this credential is for")
	id := fs.String("id", "", "replace an existing credential instead of creating one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*tool) == "" {
		return fmt.Errorf("--tool is required")
	}
	secret := os.Getenv("PROMTACT_CREDENTIAL_SECRET")
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("set PROMTACT_CREDENTIAL_SECRET to the secret to install\n" +
			"  (it is read from the environment so it does not appear in the process list or shell history)")
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	body, status, err := tenantCall(http.MethodPost, base+"/api/credentials", token, map[string]string{
		"id": *id, "tool": *tool, "header": *header, "scheme": *scheme,
		"secret": secret, "description": *description,
	})
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("storing the credential returned %d: %s", status, strings.TrimSpace(string(body)))
	}

	var stored struct {
		ID          string `json:"id"`
		Tool        string `json:"tool"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(body, &stored); err != nil {
		return fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(body)))
	}
	fmt.Printf("Stored %s for %s, fingerprint %s\n", stored.ID, stored.Tool, stored.Fingerprint)
	fmt.Println()
	fmt.Println("The gateway now presents this upstream. Remove the same secret from the agent:")
	fmt.Println("while the agent still holds it, it can still reach the tool directly and this")
	fmt.Println("changes nothing.")
	return nil
}

func credentialRemove(args []string) error {
	fs := flag.NewFlagSet("credential remove", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	id := fs.String("id", "", "credential id to remove")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("--id is required")
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}
	body, status, err := tenantCall(http.MethodDelete, base+"/api/credentials?id="+*id, token, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("removing the credential returned %d: %s", status, strings.TrimSpace(string(body)))
	}
	fmt.Printf("Removed %s. Calls for that tool fall back to the static upstream token, if one is set.\n", *id)
	return nil
}
