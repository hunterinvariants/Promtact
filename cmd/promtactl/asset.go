package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// promtactl asset — decommissioning a machine.
//
// This existed only as SQL against the database before, which is wrong for two
// reasons. A customer has no database access, so "we retired that laptop" had
// no answer at all. And deleting rows underneath a running server does not work
// the way it appears to: the process holds the same records in memory, so the
// removal seems to do nothing until a restart and can be undone by one.
//
// Going through the API removes both copies and leaves an audit record naming
// who did it — which hand-written SQL never does.

func assetCommand(args []string) error {
	if len(args) == 0 {
		assetUsage()
		return fmt.Errorf("asset requires a subcommand")
	}
	switch args[0] {
	case "remove":
		return assetRemove(args[1:])
	case "list":
		return assetList(args[1:])
	default:
		assetUsage()
		return fmt.Errorf("unknown asset subcommand %q", args[0])
	}
}

func assetUsage() {
	fmt.Fprintln(os.Stderr, "usage: promtactl asset list")
	fmt.Fprintln(os.Stderr, "       promtactl asset remove --id <asset> [--yes]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Removing an asset deletes its events, alerts and response actions.")
	fmt.Fprintln(os.Stderr, "Audit records are kept: the chain is hash-linked and cannot have")
	fmt.Fprintln(os.Stderr, "entries taken out of it.")
}

func assetList(args []string) error {
	fs := flag.NewFlagSet("asset list", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	body, status, err := tenantCall(http.MethodGet, base+"/api/assets", token, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("listing assets failed (%d): %s", status, strings.TrimSpace(string(body)))
	}
	var assets []struct {
		ID       string `json:"id"`
		Hostname string `json:"hostname"`
		Risk     int    `json:"risk_score"`
		LastSeen string `json:"last_seen"`
	}
	if err := json.Unmarshal(body, &assets); err != nil {
		return fmt.Errorf("unexpected response: %s", body)
	}
	if len(assets) == 0 {
		fmt.Println("No assets.")
		return nil
	}
	fmt.Printf("%-24s %-24s %6s  %s\n", "ASSET", "HOSTNAME", "RISK", "LAST SEEN")
	for _, asset := range assets {
		fmt.Printf("%-24s %-24s %6d  %s\n", asset.ID, asset.Hostname, asset.Risk, asset.LastSeen)
	}
	return nil
}

func assetRemove(args []string) error {
	fs := flag.NewFlagSet("asset remove", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	id := fs.String("id", "", "asset to remove (required)")
	confirm := fs.Bool("yes", false, "proceed without the confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	assetID := strings.TrimSpace(*id)
	if assetID == "" {
		return fmt.Errorf("--id is required")
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	if !*confirm {
		// The prompt is skippable, not absent. This deletes history, and a
		// scripted caller passing --yes has said so on purpose.
		fmt.Printf("This removes asset %q and every event, alert and response action recorded\n", assetID)
		fmt.Println("for it. Audit records are kept. This cannot be undone.")
		fmt.Print("\nType the asset id to confirm: ")
		var typed string
		_, _ = fmt.Scanln(&typed)
		if strings.TrimSpace(typed) != assetID {
			return fmt.Errorf("not confirmed, nothing was removed")
		}
	}

	body, status, err := tenantCall(http.MethodDelete, base+"/api/assets/"+url.PathEscape(assetID), token, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return fmt.Errorf("no asset %q in this tenant. Check the name with: promtactl asset list", assetID)
	}
	if status >= 300 {
		return fmt.Errorf("removing the asset failed (%d): %s", status, strings.TrimSpace(string(body)))
	}

	var result struct {
		AssetID string         `json:"asset_id"`
		Removed map[string]int `json:"removed"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("unexpected response: %s", body)
	}

	fmt.Printf("Removed %s:\n", result.AssetID)
	for _, key := range []string{"events", "alerts", "actions", "assets"} {
		fmt.Printf("  %-9s %d\n", key, result.Removed[key])
	}
	fmt.Println("\nAudit records were kept, including one recording this removal.")
	return nil
}
