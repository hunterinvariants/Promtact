package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// The material a walkthrough needs, so the whole demonstration can be given in
// a browser.
//
// Every step of it used to be a shell command: read the file, check its size,
// run the agent, list the outbox, read the message, print the audit trail,
// verify the chain. Nobody being sold to reads a terminal, and the person
// deciding whether to buy this is furthest of all from one.
//
// These endpoints exist only where the demonstration does.

type demoDocument struct {
	Name string `json:"name"`
	// Visible is what a person opening the file sees.
	Visible string `json:"visible"`
	// Bytes and VisibleRunes are the pair that makes the point: a file that
	// displays as 180 characters and weighs 800 bytes is carrying something.
	Bytes        int    `json:"bytes"`
	VisibleRunes int    `json:"visible_runes"`
	HiddenRunes  int    `json:"hidden_runes"`
	Hidden       string `json:"hidden,omitempty"`
}

func (a *App) handleDemoDocuments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if a.demoTools == nil {
		writeError(w, http.StatusNotFound, errors.New("this deployment was not started with --demo-tools"))
		return
	}

	entries, err := os.ReadDir(a.demoTools.Root())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	documents := make([]demoDocument, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(a.demoTools.Root(), entry.Name()))
		if err != nil {
			continue
		}
		documents = append(documents, describeDemoDocument(entry.Name(), string(raw)))
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].Name < documents[j].Name })
	writeJSON(w, http.StatusOK, documents)
}

// describeDemoDocument separates what a reader sees from what a model reads.
//
// The separation is the entire argument of the demonstration, so it is computed
// here rather than left to the browser: the invisible characters must not be
// sent inside the visible text, or the page would render the same trap it is
// trying to expose.
func describeDemoDocument(name string, content string) demoDocument {
	var visible strings.Builder
	var hidden strings.Builder
	hiddenCount := 0
	visibleCount := 0

	for _, r := range content {
		switch {
		case r >= 0xE0000 && r <= 0xE007F:
			hidden.WriteRune(r - 0xE0000)
			hiddenCount++
		case unicode.Is(unicode.Cf, r):
			hiddenCount++
		default:
			visible.WriteRune(r)
			visibleCount++
		}
	}

	return demoDocument{
		Name:         name,
		Visible:      visible.String(),
		Bytes:        len(content),
		VisibleRunes: visibleCount,
		HiddenRunes:  hiddenCount,
		Hidden:       strings.TrimSpace(hidden.String()),
	}
}

type demoMessage struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// handleDemoOutbox returns what has actually left.
//
// "Nothing was sent" is a claim; an empty outbox is the evidence for it, and in
// the run where something did leave, the message is there to be read out.
func (a *App) handleDemoOutbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if a.demoTools == nil {
		writeError(w, http.StatusNotFound, errors.New("this deployment was not started with --demo-tools"))
		return
	}

	entries, err := os.ReadDir(a.demoTools.Outbox())
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []demoMessage{})
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	messages := make([]demoMessage, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(a.demoTools.Outbox(), entry.Name()))
		if err != nil {
			continue
		}
		messages = append(messages, demoMessage{Name: entry.Name(), Body: string(raw)})
	}
	writeJSON(w, http.StatusOK, messages)
}
