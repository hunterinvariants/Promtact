package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
)

// External witnessing of the audit chain.
//
// The chain is hash-linked, so a record cannot be altered without breaking every
// hash after it. That defends against anyone who can reach the database but not
// the host. It does not defend against the operator: whoever holds root can
// rewrite the history *and* recompute the local anchor over it, because the
// anchor key lives on the same machine.
//
// So the head is published to a witness the operator does not control. The
// witness refuses to move backwards and refuses to restate an index it has
// already seen with a different head. Rewriting local history then produces a
// disagreement that anyone can check, and the operator cannot make it go away
// by editing something they own.
//
// This does not prevent an operator from reading data, and nothing on a single
// host can. What it does is remove the ability to erase the evidence afterwards.

type witnessState struct {
	Index     int       `json:"chain_index"`
	Head      string    `json:"head"`
	WitnessAt time.Time `json:"witnessed_at"`
}

type witness struct {
	endpoint string
	token    string
	client   *http.Client

	mu       sync.Mutex
	lastSeen witnessState
	lastErr  string
	mismatch bool
}

func newWitness(endpoint string, token string) *witness {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return nil
	}
	return &witness{
		endpoint: endpoint,
		token:    strings.TrimSpace(token),
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *witness) enabled() bool { return w != nil && w.endpoint != "" }

// ErrChainDiverged means the local chain and the witness disagree. It is the
// one error here that is not an operational hiccup: it says the local history
// is not the history that was witnessed.
var ErrChainDiverged = errors.New("the local audit chain disagrees with the external witness")

// Publish sends the current head to the witness and records what came back.
//
// A rejection is not retried away: if the witness refuses because the submitted
// index moves backwards or restates a known index differently, that refusal is
// the finding, and it is surfaced rather than swallowed.
func (a *App) PublishAuditAnchor(ctx context.Context) error {
	if !a.witness.enabled() {
		return nil
	}
	chain := a.store.AuditChain()

	payload, err := json.Marshal(map[string]any{
		"chain_index": chain.Linked,
		"head":        chain.Head,
		"valid":       chain.Valid,
		"at":          time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.witness.endpoint+"/anchor", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.witness.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.witness.token)
	}

	resp, err := a.witness.client.Do(req)
	if err != nil {
		a.witness.noteError(err.Error())
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	// 409 is the witness saying the submission contradicts what it already
	// holds. That is the signal this whole mechanism exists to produce.
	if resp.StatusCode == http.StatusConflict {
		a.witness.noteMismatch(string(body))
		a.recordSystemAudit("audit.anchor.diverged", "denied", map[string]string{
			"detail": sanitizeLogValue(string(body)),
		})
		return fmt.Errorf("%w: %s", ErrChainDiverged, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode >= 300 {
		message := fmt.Sprintf("witness returned %s", resp.Status)
		a.witness.noteError(message)
		return errors.New(message)
	}

	var accepted witnessState
	if err := json.Unmarshal(body, &accepted); err == nil {
		a.witness.noteAccepted(accepted)
	}
	return nil
}

// VerifyAgainstWitness compares the local chain with what the witness holds. It
// is the check an auditor runs; it reads and asserts, it does not publish.
func (a *App) VerifyAgainstWitness(ctx context.Context) (witnessState, error) {
	if !a.witness.enabled() {
		return witnessState{}, errors.New("no external witness is configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.witness.endpoint+"/anchor", nil)
	if err != nil {
		return witnessState{}, err
	}
	if a.witness.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.witness.token)
	}
	resp, err := a.witness.client.Do(req)
	if err != nil {
		return witnessState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return witnessState{}, fmt.Errorf("witness returned %s", resp.Status)
	}

	var remote witnessState
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&remote); err != nil {
		return witnessState{}, err
	}

	local := a.store.AuditChain()

	// Fewer linked records locally than were witnessed means history was
	// truncated. This is the case a local-only anchor cannot detect at all,
	// because a truncated chain re-anchors perfectly well against itself.
	if local.Linked < remote.Index {
		a.witness.noteMismatch("local chain is shorter than the witnessed chain")
		return remote, fmt.Errorf("%w: local index %d is behind witnessed index %d",
			ErrChainDiverged, local.Linked, remote.Index)
	}

	// Same length, different head: the records were rewritten in place.
	if local.Linked == remote.Index && remote.Head != "" && local.Head != remote.Head {
		a.witness.noteMismatch("head differs at the witnessed index")
		return remote, fmt.Errorf("%w: head %s does not match witnessed head %s",
			ErrChainDiverged, shortHash(local.Head), shortHash(remote.Head))
	}

	a.witness.clearMismatch()
	return remote, nil
}

// StartAuditWitness publishes on an interval until the context is cancelled.
func (a *App) StartAuditWitness(ctx context.Context, every time.Duration) {
	if !a.witness.enabled() {
		return
	}
	if every <= 0 {
		every = 5 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		// Publish once at startup so a restart is itself witnessed.
		if err := a.PublishAuditAnchor(ctx); err != nil {
			log.Printf("audit anchor: %s", sanitizeLogValue(err.Error()))
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := a.PublishAuditAnchor(ctx); err != nil {
					log.Printf("audit anchor: %s", sanitizeLogValue(err.Error()))
				}
			}
		}
	}()
}

// noteAccepted records a successful publish. It deliberately does not touch the
// mismatch flag: a divergence, once seen, is cleared only by a verification that
// agrees. Clearing it here would mean an operator could rewrite history and let
// the next interval's publish silence the alarm.
func (w *witness) noteAccepted(state witnessState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastSeen = state
	if !w.mismatch {
		w.lastErr = ""
	}
}

func (w *witness) noteError(message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastErr = message
}

// noteMismatch is sticky on purpose. A divergence must not be cleared by the
// next successful publish; only an explicit verification that agrees may clear
// it, otherwise an operator could rewrite history and wait one interval for the
// alarm to go quiet.
func (w *witness) noteMismatch(message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.mismatch = true
	w.lastErr = message
}

func (w *witness) clearMismatch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.mismatch = false
	w.lastErr = ""
}

func (w *witness) status() (witnessState, bool, string) {
	if !w.enabled() {
		return witnessState{}, false, ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastSeen, w.mismatch, w.lastErr
}

func shortHash(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

// recordSystemAudit writes an audit entry for something the system did on its
// own, with no request behind it. It goes through the same chain as every other
// record, which is what makes a divergence report itself witnessed.
func (a *App) recordSystemAudit(action string, outcome string, metadata map[string]string) {
	a.recordAudit(nil, auth.Principal{Name: "system", Tenant: "default", Roles: []string{auth.RoleAdmin}},
		action, "audit_chain", "", outcome, metadata)
}

// requestUserAgent tolerates the absent request that a system-initiated audit
// record carries, for the same reason sourceIP does.
func requestUserAgent(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.UserAgent()
}

// handleAuditWitness reports the witnessed state and re-checks it live. It is
// admin-only: the answer says how far the evidence trail is independently
// corroborated, which is not a customer-facing detail.
func (a *App) handleAuditWitness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !a.witness.enabled() {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured": false,
			"note":       "No external witness is configured; the audit chain is anchored locally only, which does not protect against an operator with host access.",
		})
		return
	}

	local := a.store.AuditChain()
	remote, err := a.VerifyAgainstWitness(r.Context())
	_, diverged, lastError := a.witness.status()

	body := map[string]any{
		"configured":      true,
		"local_index":     local.Linked,
		"local_head":      local.Head,
		"local_valid":     local.Valid,
		"witnessed_index": remote.Index,
		"witnessed_head":  remote.Head,
		"witnessed_at":    remote.WitnessAt,
		"agrees":          err == nil,
		"diverged":        diverged,
		"last_error":      lastError,
	}
	// A divergence is not a server error: the endpoint worked exactly as
	// intended. It is a conflict between two records, and the caller needs the
	// detail rather than a bare failure.
	if errors.Is(err, ErrChainDiverged) {
		body["detail"] = err.Error()
		writeJSON(w, http.StatusConflict, body)
		return
	}
	if err != nil {
		body["detail"] = err.Error()
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	writeJSON(w, http.StatusOK, body)
}
