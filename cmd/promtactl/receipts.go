package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/hunterinvariants/promtact/internal/witness"
)

// promtactl audit receipts - check the witness's signed statements, offline.
//
// This is the command an auditor runs, and the reason it exists is what it does
// *not* need: it does not need the witness to be reachable, and it does not
// need to trust the server that hands over the receipts. The public key is
// supplied by the caller, the signature is checked locally, and a server that
// altered a receipt produces a failure rather than a different answer.
//
// That property is the whole argument. "Ask our witness and it will confirm"
// requires trusting a service the vendor runs. "Here is a signature you can
// check yourself, with a key you already hold" does not.

func auditReceipts(args []string) error {
	fs := flag.NewFlagSet("audit receipts", flag.ContinueOnError)
	common := tenantCommonFlags(fs)
	publicKey := fs.String("public-key", os.Getenv("PROMTACT_WITNESS_PUBLIC_KEY"),
		"witness public key, base64 SPKI or a PEM block; @path reads it from a file")
	showAll := fs.Bool("all", false, "list every receipt rather than a summary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	base, token, err := common.resolve()
	if err != nil {
		return err
	}

	body, status, err := tenantCall(http.MethodGet, base+"/api/audit/receipts", token, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("reading the receipts failed (%d): %s", status, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Receipts []witness.Receipt `json:"receipts"`
		Count    int               `json:"count"`
		Signed   int               `json:"signed"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(body)))
	}

	fmt.Printf("Receipts     %d stored, %d signed\n", payload.Count, payload.Signed)
	if payload.Count == 0 {
		fmt.Println()
		fmt.Println("             Nothing has been witnessed yet, so there is nothing here")
		fmt.Println("             that an auditor could check without this server.")
		return nil
	}

	keyValue, err := readPublicKey(*publicKey)
	if err != nil {
		return err
	}
	if strings.TrimSpace(keyValue) == "" {
		fmt.Println()
		fmt.Println("             Supply the witness public key to check them:")
		fmt.Println("               promtactl audit receipts --public-key @witness.pub")
		fmt.Println()
		fmt.Println("             Fetch it once from the witness itself, at /anchor/pubkey,")
		fmt.Println("             and keep it. The point of a receipt is that checking it")
		fmt.Println("             later needs the key and nothing else.")
		return nil
	}

	key, err := witness.ParsePublicKey(keyValue)
	if err != nil {
		return err
	}
	result := witness.VerifyAll(payload.Receipts, key)

	fmt.Printf("Checked      %d\n", result.Checked)
	fmt.Printf("Valid        %d\n", result.Valid)
	if result.Unsigned > 0 {
		fmt.Printf("Unsigned     %d  (witnessed before receipt signing was enabled)\n", result.Unsigned)
	}

	if *showAll {
		fmt.Println()
		for _, receipt := range payload.Receipts {
			state := "unsigned"
			if receipt.Signed() {
				if err := receipt.Verify(key); err == nil {
					state = "valid"
				} else {
					state = "INVALID"
				}
			}
			when := receipt.WitnessedAt
			if parsed, err := receipt.WitnessedTime(); err == nil {
				when = parsed.Format("2006-01-02 15:04:05")
			}
			fmt.Printf("  #%-6d %-8s %s  %s\n", receipt.ChainIndex, state,
				truncate(receipt.Head, 24), when)
		}
	}

	if len(result.Failures) > 0 {
		fmt.Println()
		fmt.Println("SIGNATURES DID NOT VERIFY:")
		for _, failure := range result.Failures {
			fmt.Printf("  %s\n", failure)
		}
		fmt.Println()
		fmt.Println("             A stored receipt is not one this witness key produced.")
		fmt.Println("             Either the receipt was altered after it was stored, or it")
		fmt.Println("             never came from this witness. Both are findings.")
		return fmt.Errorf("%d receipt(s) failed verification", len(result.Failures))
	}

	if result.Valid > 0 {
		fmt.Println()
		fmt.Printf("             A third party signed for record %d and every record\n", result.HighestWitnessed)
		fmt.Println("             below it that carries a receipt. That signature was just")
		fmt.Println("             checked here, against a key you hold - not by asking the")
		fmt.Println("             witness, and not by trusting this server.")
		fmt.Println()
		fmt.Println("             Records above that index are not covered. A range with no")
		fmt.Println("             receipt was never witnessed, which is a gap rather than a")
		fmt.Println("             reassurance.")
	}
	return nil
}

// readPublicKey resolves @path to file contents, so a key can live in a file
// rather than in shell history.
func readPublicKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "@") {
		return value, nil
	}
	data, err := os.ReadFile(strings.TrimPrefix(value, "@"))
	if err != nil {
		return "", fmt.Errorf("reading the public key: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}
