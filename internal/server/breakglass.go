package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// Break-glass: announcing direct access to the host or the database before
// taking it.
//
// This is deliberately not a credential issuer. Handing out database logins
// would mean the application must be allowed to administer roles, and that
// privilege is a larger risk than the one being addressed. The credential stays
// where it is; what changes is that using it is announced first.
//
// An announcement on its own is voluntary, and an operator who wanted to hide
// would simply skip it. It is worth something anyway — it makes the ordinary,
// legitimate case attributable — but the control only closes when it is paired
// with independent observation: database sessions are logged off-host, and a
// session with no matching announcement is itself the finding. Neither half is
// sufficient; the pair is.
//
// Both halves land outside the operator's reach: the announcement enters the
// audit chain and is carried to the external witness, and the observation goes
// to the same witness directly.

const (
	breakglassMinReason = 12
	breakglassMaxWindow = 8 * time.Hour
)

type breakglassSession struct {
	ID        string     `json:"id"`
	Actor     string     `json:"actor"`
	Reason    string     `json:"reason"`
	OpenedAt  time.Time  `json:"opened_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

type breakglassRegister struct {
	mu   sync.Mutex
	open map[string]breakglassSession
}

func newBreakglassRegister() *breakglassRegister {
	return &breakglassRegister{open: map[string]breakglassSession{}}
}

// Open records an announced access window. The register is in memory on
// purpose: it is a convenience for the reconciler, not the evidence. The
// evidence is the audit record, which is durable and witnessed.
func (b *breakglassRegister) Open(session breakglassSession) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune(time.Now().UTC())
	b.open[session.ID] = session
}

func (b *breakglassRegister) Close(id string, at time.Time) (breakglassSession, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	session, ok := b.open[id]
	if !ok {
		return breakglassSession{}, false
	}
	session.ClosedAt = &at
	delete(b.open, id)
	return session, true
}

// Covers reports whether an announced window was in force at the given time.
// This is the question the reconciler asks of every observed database session.
func (b *breakglassRegister) Covers(at time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, session := range b.open {
		if !at.Before(session.OpenedAt) && !at.After(session.ExpiresAt) {
			return true
		}
	}
	return false
}

func (b *breakglassRegister) List() []breakglassSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune(time.Now().UTC())
	sessions := make([]breakglassSession, 0, len(b.open))
	for _, session := range b.open {
		sessions = append(sessions, session)
	}
	return sessions
}

func (b *breakglassRegister) prune(now time.Time) {
	for id, session := range b.open {
		// A window that expired long ago is dropped, but only well after its
		// end: a session observed slightly late must still find its cover.
		if now.After(session.ExpiresAt.Add(time.Hour)) {
			delete(b.open, id)
		}
	}
}

// handleBreakglass opens or lists announced access windows.
func (a *App) handleBreakglass(w http.ResponseWriter, r *http.Request) {
	principal := principalFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"open": a.breakglass.List()})

	case http.MethodPost:
		var req struct {
			Reason  string `json:"reason"`
			Minutes int    `json:"minutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		reason := strings.TrimSpace(req.Reason)
		// A reason that says nothing is worse than none: it produces a record
		// that looks like accountability and carries no information.
		if len(reason) < breakglassMinReason {
			writeError(w, http.StatusBadRequest, fmt.Errorf(
				"a reason of at least %d characters is required", breakglassMinReason))
			return
		}

		window := time.Duration(req.Minutes) * time.Minute
		if window <= 0 {
			window = 30 * time.Minute
		}
		if window > breakglassMaxWindow {
			// An open-ended window is indistinguishable from no control at all.
			writeError(w, http.StatusBadRequest, fmt.Errorf(
				"the window may not exceed %s", breakglassMaxWindow))
			return
		}

		now := time.Now().UTC()
		session := breakglassSession{
			ID:        a.nextID("bg"),
			Actor:     principal.Name,
			Reason:    reason,
			OpenedAt:  now,
			ExpiresAt: now.Add(window),
		}
		a.breakglass.Open(session)

		// The audit record is the durable half and reaches the witness on the
		// next anchor. It is written before the alert so a failure to notify
		// cannot lose the record.
		a.recordAudit(r, principal, "operator.breakglass.opened", "host", session.ID, "accepted",
			map[string]string{
				"reason":     sanitizeLogValue(reason),
				"expires_at": session.ExpiresAt.Format(time.RFC3339),
			})
		a.notifyBreakglass(session, "opened")

		writeJSON(w, http.StatusCreated, session)

	default:
		methodNotAllowed(w)
	}
}

// handleBreakglassClose ends an announced window early.
func (a *App) handleBreakglassClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	principal := principalFromRequest(r)
	id := strings.TrimPrefix(r.URL.Path, "/api/admin/breakglass/")
	id = strings.TrimSuffix(id, "/close")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, errors.New("unknown break-glass session"))
		return
	}

	session, ok := a.breakglass.Close(id, time.Now().UTC())
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("no such open break-glass session"))
		return
	}
	a.recordAudit(r, principal, "operator.breakglass.closed", "host", session.ID, "accepted",
		map[string]string{"reason": sanitizeLogValue(session.Reason)})
	a.notifyBreakglass(session, "closed")
	writeJSON(w, http.StatusOK, session)
}

// notifyBreakglass sends the announcement outward. Delivery failure is logged
// and not propagated: the caller must not be told the announcement failed when
// the durable record already exists.
func (a *App) notifyBreakglass(session breakglassSession, event string) {
	if a.webhook.URL == "" {
		return
	}
	alert := domain.Alert{
		ID:          a.nextID("alr"),
		RuleID:      "operator.breakglass",
		Title:       fmt.Sprintf("Operator break-glass %s: %s", event, session.Actor),
		Description: session.Reason,
		Severity:    domain.SeverityHigh,
		Status:      domain.AlertOpen,
		CreatedAt:   time.Now().UTC(),
		Evidence: map[string]string{
			"session":    session.ID,
			"actor":      session.Actor,
			"event":      event,
			"expires_at": session.ExpiresAt.Format(time.RFC3339),
		},
	}
	go func() {
		if err := a.webhook.ExportAlerts([]domain.Alert{alert}); err != nil {
			log.Printf("break-glass notification failed: %s", sanitizeLogValue(err.Error()))
		}
	}()
}
