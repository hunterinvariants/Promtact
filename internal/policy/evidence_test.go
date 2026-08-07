package policy

import (
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// An alert that cannot say what it saw is worse than no alert.
//
// The failure these tests pin down was invisible from the code: the discovery
// rule matched on four sources — command, process, signal, raw record text —
// and then reported two of them. For a Windows event log record, where the
// substance sits in the message, both were empty. The console duly rendered an
// alert whose guidance said "look at the command under Evidence" above an
// evidence block that had been filtered away to nothing.
//
// Nothing failed. A high-severity finding simply arrived with its contents
// missing, and the only way to discover that was to read one in the console and
// notice there was nothing to read.

func TestDiscoveryAlertCarriesEvidenceFromMessageOnly(t *testing.T) {
	engine := New(Config{})

	// A Windows record as the collector forwards it when the log does not break
	// the fields out: process and command empty, everything in the message.
	alerts := engine.Evaluate(domain.Event{
		ID:      "evt-win-1",
		Kind:    domain.EventProcessStart,
		AssetID: "laptop-1",
		Metadata: map[string]string{
			"collector": "windows-eventlog",
			"message":   "A new process has been created.\n\nDetailed information about the process that was started, which ran whoami /groups to enumerate the account.",
		},
	})

	alert := findRule(t, alerts, "process.discovery.chain")

	if len(alert.Evidence) == 0 {
		t.Fatal("discovery alert carries no evidence at all; the console would render an empty block under guidance that points at it")
	}
	if alert.Evidence["matched"] == "" {
		t.Error("evidence does not record which term matched, so the reader cannot tell why this fired")
	}
	observed := alert.Evidence["observed"]
	if observed == "" {
		t.Fatal("no structured field survived and the matching text was not quoted either")
	}
	if !strings.Contains(strings.ToLower(observed), alert.Evidence["matched"]) {
		t.Errorf("quoted text %q does not contain the matched term %q, so it is not the evidence for this finding",
			observed, alert.Evidence["matched"])
	}
}

func TestDiscoveryAlertPrefersStructuredFields(t *testing.T) {
	engine := New(Config{})

	alerts := engine.Evaluate(domain.Event{
		ID:      "evt-win-2",
		Kind:    domain.EventProcessStart,
		AssetID: "laptop-1",
		Process: `C:\Windows\System32\whoami.exe`,
		Command: "whoami /groups",
		Actor:   "ACME\\rmueller",
	})

	alert := findRule(t, alerts, "process.discovery.chain")

	for _, key := range []string{"process", "command", "account", "matched"} {
		if alert.Evidence[key] == "" {
			t.Errorf("evidence is missing %q, which the console shows and the guidance names", key)
		}
	}
	// When Windows supplied the fields, quoting the raw text as well would only
	// repeat them.
	if _, present := alert.Evidence["observed"]; present {
		t.Error("raw text was quoted even though the structured fields were populated")
	}
}

// excerptAround is what stands between the reader and several hundred
// characters of Windows boilerplate, so it has to keep the match in view.
func TestExcerptKeepsTheMatchInView(t *testing.T) {
	text := strings.Repeat("boilerplate preamble. ", 40) + "ran net group /domain here" +
		strings.Repeat(" trailing detail.", 40)

	excerpt := excerptAround(text, "net group")

	if !strings.Contains(excerpt, "net group") {
		t.Fatalf("excerpt dropped the match: %q", excerpt)
	}
	if len(excerpt) > 220 {
		t.Errorf("excerpt is %d characters; it is meant to show the finding, not bury it", len(excerpt))
	}
}

func findRule(t *testing.T, alerts []domain.Alert, ruleID string) domain.Alert {
	t.Helper()
	for _, alert := range alerts {
		if alert.RuleID == ruleID {
			return alert
		}
	}
	t.Fatalf("no %s alert was raised", ruleID)
	return domain.Alert{}
}
