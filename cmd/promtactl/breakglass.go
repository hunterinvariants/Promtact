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

// promtactl breakglass — announce direct access to the host or database before
// taking it.
//
// The announcement is recorded in the audit chain, carried to the external
// witness, and pushed as an alert. It issues no credential: the point is not to
// grant access but to make taking it visible, and doing that without letting
// the application administer database roles keeps the privilege boundary intact.
//
// Skipping this command is possible. That is why database sessions are logged
// off-host as well: a session with no announcement covering it is the finding.

type breakglassSession struct {
	ID        string    `json:"id"`
	Actor     string    `json:"actor"`
	Reason    string    `json:"reason"`
	OpenedAt  time.Time `json:"opened_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func runBreakglass(args []string) {
	fs := flag.NewFlagSet("breakglass", flag.ExitOnError)
	baseURL := fs.String("url", firstNonEmpty(os.Getenv("PROMTACT_URL"), "http://127.0.0.1:8080"), "Promtact base URL")
	token := fs.String("token", os.Getenv("PROMTACT_API_TOKEN"), "admin API token")
	tokenFile := fs.String("token-file", "", "read the admin token from a file instead")
	reason := fs.String("reason", "", "why direct access is needed (required, recorded permanently)")
	minutes := fs.Int("minutes", 30, "how long the announced window lasts")
	closeID := fs.String("close", "", "close an announced window early, by id")
	list := fs.Bool("list", false, "list currently announced windows")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: promtactl breakglass --reason \"...\" [--minutes 30]")
		fmt.Fprintln(os.Stderr, "       promtactl breakglass --list")
		fmt.Fprintln(os.Stderr, "       promtactl breakglass --close bg-...")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Announces direct host or database access. The announcement is written to the")
		fmt.Fprintln(os.Stderr, "audit chain, witnessed externally, and alerted. No credential is issued.")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	apiToken := strings.TrimSpace(*token)
	if path := strings.TrimSpace(*tokenFile); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "breakglass: reading token file: %v\n", err)
			os.Exit(1)
		}
		apiToken = strings.TrimSpace(string(data))
	}

	client := &http.Client{Timeout: 15 * time.Second}
	base := strings.TrimRight(strings.TrimSpace(*baseURL), "/")

	switch {
	case *list:
		body, status, err := breakglassCall(client, http.MethodGet, base+"/api/admin/breakglass", apiToken, nil)
		if err != nil || status >= 300 {
			fmt.Fprintf(os.Stderr, "breakglass: list failed (%d): %v %s\n", status, err, body)
			os.Exit(1)
		}
		fmt.Println(string(body))

	case strings.TrimSpace(*closeID) != "":
		id := strings.TrimSpace(*closeID)
		body, status, err := breakglassCall(client, http.MethodPost,
			base+"/api/admin/breakglass/"+id+"/close", apiToken, nil)
		if err != nil || status >= 300 {
			fmt.Fprintf(os.Stderr, "breakglass: close failed (%d): %v %s\n", status, err, body)
			os.Exit(1)
		}
		fmt.Printf("Window %s closed.\n", id)

	default:
		if strings.TrimSpace(*reason) == "" {
			fs.Usage()
			fmt.Fprintln(os.Stderr, "\nbreakglass: --reason is required")
			os.Exit(2)
		}
		payload, _ := json.Marshal(map[string]any{"reason": *reason, "minutes": *minutes})
		body, status, err := breakglassCall(client, http.MethodPost, base+"/api/admin/breakglass", apiToken, payload)
		if err != nil || status >= 300 {
			fmt.Fprintf(os.Stderr, "breakglass: open failed (%d): %v %s\n", status, err, body)
			os.Exit(1)
		}

		var session breakglassSession
		if err := json.Unmarshal(body, &session); err != nil {
			fmt.Fprintf(os.Stderr, "breakglass: unexpected response: %s\n", body)
			os.Exit(1)
		}

		// The output states plainly what was and was not done, so nobody is left
		// believing this granted them something.
		fmt.Printf("Break-glass window %s opened by %s.\n", session.ID, session.Actor)
		fmt.Printf("  Reason:  %s\n", session.Reason)
		fmt.Printf("  Expires: %s\n", session.ExpiresAt.Format(time.RFC3339))
		fmt.Println()
		fmt.Println("Recorded in the audit chain and published to the external witness.")
		fmt.Println("No credential was issued; this announces access, it does not grant it.")
		fmt.Printf("Close it early with: promtactl breakglass --close %s\n", session.ID)
	}
}

func breakglassCall(client *http.Client, method string, url string, token string, payload []byte) ([]byte, int, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return body, resp.StatusCode, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
