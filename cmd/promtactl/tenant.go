package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// promtactl tenant — provisioning a customer without reading JSON by eye.
//
// The admin API returns the whole record, which is right for a program and
// wrong for a person: onboarding someone should not mean hunting for a field
// in a wall of text. These commands call the same endpoints and print what a
// provider actually has to hand over.

func tenantCommand(args []string) error {
	if len(args) == 0 {
		tenantUsage()
		return fmt.Errorf("tenant requires a subcommand")
	}
	switch args[0] {
	case "create":
		return tenantCreate(args[1:])
	case "add-agent":
		return tenantAddAgent(args[1:])
	case "list":
		return tenantList(args[1:])
	case "new-key":
		return tenantNewKey(args[1:])
	default:
		tenantUsage()
		return fmt.Errorf("unknown tenant subcommand %q", args[0])
	}
}

func tenantUsage() {
	fmt.Fprintln(os.Stderr, "usage: promtactl tenant create   --name acme --display \"Acme GmbH\"")
	fmt.Fprintln(os.Stderr, "       promtactl tenant add-agent --tenant acme")
	fmt.Fprintln(os.Stderr, "       promtactl tenant list")
	fmt.Fprintln(os.Stderr, "       promtactl tenant new-key   --tenant acme --user acme-agent")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Every command prints what has to be handed over, and says so plainly")
	fmt.Fprintln(os.Stderr, "when a key cannot be shown again.")
}

type tenantFlags struct {
	baseURL *string
	token   *string
	file    *string
}

func tenantCommonFlags(fs *flag.FlagSet) tenantFlags {
	return tenantFlags{
		baseURL: fs.String("url", firstNonEmpty(os.Getenv("PROMTACT_URL"), "http://127.0.0.1:8080"), "Promtact base URL"),
		token:   fs.String("token", os.Getenv("PROMTACT_ADMIN_TOKEN"), "admin API token"),
		file:    fs.String("token-file", "", "read the admin token from a file instead"),
	}
}

func (f tenantFlags) resolve() (string, string, error) {
	token := strings.TrimSpace(*f.token)
	if path := strings.TrimSpace(*f.file); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", "", fmt.Errorf("reading --token-file: %w", err)
		}
		token = strings.TrimSpace(string(data))
	}
	if token == "" {
		return "", "", fmt.Errorf("an admin token is required: pass --token, --token-file, or set PROMTACT_ADMIN_TOKEN")
	}
	return strings.TrimRight(strings.TrimSpace(*f.baseURL), "/"), token, nil
}

func tenantCreate(args []string) error {
	fs := flag.NewFlagSet("tenant create", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	name := fs.String("name", "", "tenant slug, short and permanent (required)")
	display := fs.String("display", "", "display name shown in the console")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name is required")
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	body, status, err := tenantCall(http.MethodPost, base+"/api/admin/tenants", token, map[string]any{
		"tenant":       strings.TrimSpace(*name),
		"display_name": strings.TrimSpace(*display),
	})
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("creating the tenant failed (%d): %s", status, strings.TrimSpace(string(body)))
	}

	var created struct {
		APIKey string `json:"api_key"`
		User   struct {
			Name  string   `json:"name"`
			Roles []string `json:"roles"`
		} `json:"user"`
		Tenant struct {
			Tenant      string `json:"tenant"`
			DisplayName string `json:"display_name"`
		} `json:"tenant"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return fmt.Errorf("unexpected response: %s", body)
	}

	fmt.Printf("Tenant %s created.\n\n", created.Tenant.Tenant)
	fmt.Println("Console access — give this to the customer:")
	fmt.Printf("  Address   %s\n", publicAddressHint(base))
	fmt.Printf("  User      %s\n", created.User.Name)
	fmt.Printf("  Key       %s\n", created.APIKey)
	fmt.Printf("  Roles     %s\n", strings.Join(created.User.Roles, ", "))
	fmt.Println()
	fmt.Println("This key is shown once and cannot be recovered. Write it down now.")
	fmt.Printf("\nNext: promtactl tenant add-agent --tenant %s\n", created.Tenant.Tenant)
	return nil
}

func tenantAddAgent(args []string) error {
	fs := flag.NewFlagSet("tenant add-agent", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	tenant := fs.String("tenant", "", "tenant slug (required)")
	name := fs.String("name", "", "account name (defaults to <tenant>-agent)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	slug := strings.TrimSpace(*tenant)
	if slug == "" {
		return fmt.Errorf("--tenant is required")
	}
	account := strings.TrimSpace(*name)
	if account == "" {
		account = slug + "-agent"
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	body, status, err := tenantCall(http.MethodPost, base+"/api/admin/tenants/"+slug+"/users", token, map[string]any{
		"name":  account,
		"roles": []string{"ingestor"},
	})
	if err != nil {
		return err
	}
	if status >= 300 {
		// The most common failure by far, and the message from the database is
		// not one a person should have to decode.
		if strings.Contains(string(body), "duplicate key") {
			return fmt.Errorf("an account named %q already exists. Issue a new key instead:\n  promtactl tenant new-key --tenant %s --user %s",
				account, slug, account)
		}
		return fmt.Errorf("creating the agent account failed (%d): %s", status, strings.TrimSpace(string(body)))
	}

	var created struct {
		APIKey string `json:"api_key"`
		User   struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return fmt.Errorf("unexpected response: %s", body)
	}

	fmt.Printf("Agent account %s created for tenant %s.\n\n", created.User.Name, slug)
	fmt.Println("Agent key — this goes on the endpoint, by a different channel")
	fmt.Println("than the console key:")
	fmt.Printf("  Key       %s\n", created.APIKey)
	fmt.Println()
	fmt.Println("This key is shown once and cannot be recovered. Write it down now.")
	fmt.Println("It may submit telemetry and nothing else: it cannot read alerts,")
	fmt.Println("approve actions, or provision anything.")
	return nil
}

func tenantList(args []string) error {
	fs := flag.NewFlagSet("tenant list", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	body, status, err := tenantCall(http.MethodGet, base+"/api/admin/tenants", token, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("listing tenants failed (%d): %s", status, strings.TrimSpace(string(body)))
	}

	var accounts []struct {
		Tenant      string `json:"tenant"`
		DisplayName string `json:"display_name"`
		Status      string `json:"status"`
		Plan        string `json:"plan"`
	}
	if err := json.Unmarshal(body, &accounts); err != nil {
		return fmt.Errorf("unexpected response: %s", body)
	}
	if len(accounts) == 0 {
		fmt.Println("No tenants.")
		return nil
	}
	fmt.Printf("%-20s %-26s %-10s %s\n", "TENANT", "NAME", "STATUS", "PLAN")
	for _, account := range accounts {
		fmt.Printf("%-20s %-26s %-10s %s\n", account.Tenant, account.DisplayName, account.Status, account.Plan)
	}
	return nil
}

func tenantNewKey(args []string) error {
	fs := flag.NewFlagSet("tenant new-key", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	tenant := fs.String("tenant", "", "tenant slug (required)")
	user := fs.String("user", "", "account name (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	slug, account := strings.TrimSpace(*tenant), strings.TrimSpace(*user)
	if slug == "" || account == "" {
		return fmt.Errorf("--tenant and --user are both required")
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	body, status, err := tenantCall(http.MethodGet, base+"/api/admin/tenants/"+slug+"/users", token, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("listing users failed (%d): %s", status, strings.TrimSpace(string(body)))
	}
	var users []struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(body, &users); err != nil {
		return fmt.Errorf("unexpected response: %s", body)
	}

	id := ""
	for _, u := range users {
		if strings.EqualFold(u.Name, account) {
			id = u.ID
		}
	}
	if id == "" {
		names := make([]string, 0, len(users))
		for _, u := range users {
			names = append(names, u.Name)
		}
		return fmt.Errorf("no account named %q in tenant %s. Accounts here: %s",
			account, slug, strings.Join(names, ", "))
	}

	body, status, err = tenantCall(http.MethodPost, base+"/api/admin/tenants/"+slug+"/keys", token, map[string]any{
		"user_id": id,
		"name":    "replacement " + time.Now().UTC().Format("2006-01-02"),
	})
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("issuing the key failed (%d): %s", status, strings.TrimSpace(string(body)))
	}
	var issued struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(body, &issued); err != nil {
		return fmt.Errorf("unexpected response: %s", body)
	}

	fmt.Printf("New key for %s in tenant %s:\n\n", account, slug)
	fmt.Printf("  Key       %s\n\n", issued.APIKey)
	fmt.Println("Shown once. The account's older keys still work until you revoke them.")
	return nil
}

func tenantCall(method string, url string, token string, payload any) ([]byte, int, error) {
	var reader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, nil
}

// publicAddressHint prefers the address a customer would actually type over the
// loopback one an operator ran the command against.
func publicAddressHint(base string) string {
	if strings.Contains(base, "127.0.0.1") || strings.Contains(base, "localhost") {
		if public := strings.TrimSpace(os.Getenv("PROMTACT_PUBLIC_URL")); public != "" {
			return public
		}
		return base + "   (this is the local address; give the customer the public one)"
	}
	return base
}
