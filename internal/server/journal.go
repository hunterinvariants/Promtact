package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The gateway's verdict is computed locally and does not depend on the
// database. Persistence does. Without a journal a storage outage would discard
// an already-decided verdict and answer 500 — which is worst for a deny, since
// a caller that treats an error as "gateway unavailable" would proceed with the
// very call the gateway had just blocked.
//
// The journal keeps those records on local disk so enforcement continues during
// an outage and the audit trail is reconciled afterwards, rather than lost.

type journalEntry struct {
	Time    time.Time       `json:"time"`
	Kind    string          `json:"kind"`
	Tenant  string          `json:"tenant,omitempty"`
	Reason  string          `json:"reason,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

const (
	journalKindAlerts  = "alerts"
	journalKindActions = "actions"
)

type decisionJournal struct {
	mu      sync.Mutex
	path    string
	max     int
	depth   int
	dropped int
}

func newDecisionJournal(path string, max int) *decisionJournal {
	if max <= 0 {
		max = 10000
	}
	journal := &decisionJournal{path: strings.TrimSpace(path), max: max}
	journal.depth = journal.countLines()
	return journal
}

func (j *decisionJournal) enabled() bool {
	return j != nil && j.path != ""
}

func (j *decisionJournal) countLines() int {
	if j.path == "" {
		return 0
	}
	file, err := os.Open(j.path)
	if err != nil {
		return 0
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count
}

// Append writes one record durably. The entry is fsynced before returning, so a
// crash right after a served decision cannot lose the record that explains it.
func (j *decisionJournal) Append(kind string, tenant string, reason string, payload any) error {
	if !j.enabled() {
		return errors.New("decision journal is not configured")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	line, err := json.Marshal(journalEntry{
		Time:    time.Now().UTC(),
		Kind:    kind,
		Tenant:  tenant,
		Reason:  reason,
		Payload: raw,
	})
	if err != nil {
		return err
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.depth >= j.max {
		// Refuse rather than rotate: silently discarding the oldest security
		// records would be the same data loss the journal exists to prevent.
		j.dropped++
		return errors.New("decision journal is full")
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	j.depth++
	return nil
}

func (j *decisionJournal) Depth() int {
	if !j.enabled() {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.depth
}

func (j *decisionJournal) Dropped() int {
	if !j.enabled() {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.dropped
}

// Drain replays the journal through apply. Entries that apply cleanly are
// removed; the first failure stops the drain and keeps that entry and every
// later one, so ordering is preserved and nothing is dropped on a partial
// recovery. It returns how many entries were reconciled.
func (j *decisionJournal) Drain(apply func(journalEntry) error) (int, error) {
	if !j.enabled() {
		return 0, nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	file, err := os.Open(j.path)
	if err != nil {
		if os.IsNotExist(err) {
			j.depth = 0
			return 0, nil
		}
		return 0, err
	}
	entries := make([]journalEntry, 0, 64)
	rawLines := make([]string, 0, 64)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var entry journalEntry
		if err := json.Unmarshal([]byte(text), &entry); err != nil {
			// A corrupt line must not block reconciliation of the rest, but it
			// is kept so the corruption stays visible for investigation.
			rawLines = append(rawLines, text)
			entries = append(entries, journalEntry{})
			continue
		}
		rawLines = append(rawLines, text)
		entries = append(entries, entry)
	}
	file.Close()
	if err := scanner.Err(); err != nil {
		return 0, err
	}

	applied := 0
	for i, entry := range entries {
		if entry.Kind == "" {
			break // corrupt line: stop and keep everything from here on
		}
		if err := apply(entry); err != nil {
			break
		}
		applied = i + 1
	}
	if applied == 0 {
		return 0, nil
	}

	remaining := rawLines[applied:]
	if len(remaining) == 0 {
		if err := os.Remove(j.path); err != nil && !os.IsNotExist(err) {
			return applied, err
		}
		j.depth = 0
		return applied, nil
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(remaining, "\n")+"\n"), 0o600); err != nil {
		return applied, err
	}
	if err := os.Rename(tmp, j.path); err != nil {
		return applied, err
	}
	j.depth = len(remaining)
	return applied, nil
}
