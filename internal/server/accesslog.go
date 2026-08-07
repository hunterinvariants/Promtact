package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// Observed database sessions, reconciled against announced access.
//
// Break-glass says what an operator intends to do. This says what actually
// happened, and it comes from Postgres rather than from the operator: a shipper
// reads the database's own connection log and submits each session that is not
// the application itself.
//
// The reconciliation is the control. A session covered by an announced window is
// ordinary and recorded. A session with no announcement is the finding, and it
// produces an audit record and an alert — the case this whole mechanism exists
// to make visible.
//
// The shipper runs on the host, so an operator can stop it. That is why it also
// sends a heartbeat: silence is not the same as nothing happening, and a
// reconciler that only reacts to what arrives can be silenced by arranging for
// nothing to arrive.

type observedSession struct {
	At          time.Time `json:"at"`
	User        string    `json:"user"`
	Application string    `json:"application"`
	Source      string    `json:"source"`
	Database    string    `json:"database"`
	Event       string    `json:"event"`
}

type accessLogState struct {
	mu            sync.Mutex
	lastHeartbeat time.Time
	observed      int
	unannounced   int
}

func (s *accessLogState) noteHeartbeat(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHeartbeat = at
}

func (s *accessLogState) noteSession(announced bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observed++
	if !announced {
		s.unannounced++
	}
}

func (s *accessLogState) snapshot() (time.Time, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastHeartbeat, s.observed, s.unannounced
}

// handleAccessLog receives observed database sessions from the host shipper.
func (a *App) handleAccessLog(w http.ResponseWriter, r *http.Request) {
	principal := principalFromRequest(r)

	if r.Method == http.MethodGet {
		last, observed, unannounced := a.accessLog.snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"last_heartbeat":       last,
			"sessions_observed":    observed,
			"sessions_unannounced": unannounced,
			"shipper_silent":       a.accessLogSilent(),
		})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var submission struct {
		Heartbeat bool              `json:"heartbeat"`
		Sessions  []observedSession `json:"sessions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&submission); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Every submission counts as a heartbeat, so a busy shipper does not have to
	// send one separately.
	a.accessLog.noteHeartbeat(time.Now().UTC())

	unannounced := 0
	for _, session := range submission.Sessions {
		if session.At.IsZero() {
			session.At = time.Now().UTC()
		}
		// The application's own connections are the expected traffic and are not
		// what this watches. They are identified by the application name the
		// service sets on its connection string.
		if strings.EqualFold(strings.TrimSpace(session.Application), applicationName) {
			continue
		}

		covered := a.breakglass.Covers(session.At)
		a.accessLog.noteSession(covered)
		if covered {
			a.recordAudit(r, principal, "operator.database.session", "database", session.User, "announced",
				accessLogMetadata(session))
			continue
		}

		unannounced++
		// An unannounced session is the finding. It is recorded before it is
		// alerted, so a failed notification cannot lose it.
		a.recordAudit(r, principal, "operator.database.session", "database", session.User, "unannounced",
			accessLogMetadata(session))
		a.alertUnannouncedSession(session)
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"received":    len(submission.Sessions),
		"unannounced": unannounced,
	})
}

func accessLogMetadata(session observedSession) map[string]string {
	return map[string]string{
		"db_user":     sanitizeLogValue(session.User),
		"application": sanitizeLogValue(session.Application),
		"source":      sanitizeLogValue(session.Source),
		"database":    sanitizeLogValue(session.Database),
		"event":       sanitizeLogValue(session.Event),
		"at":          session.At.UTC().Format(time.RFC3339),
	}
}

func (a *App) alertUnannouncedSession(session observedSession) {
	if a.webhook.URL == "" {
		return
	}
	alert := domain.Alert{
		ID:          a.nextID("alr"),
		RuleID:      "operator.database.unannounced",
		Title:       fmt.Sprintf("Unannounced database session by %s", session.User),
		Description: "A database session was observed with no break-glass window covering it.",
		Severity:    domain.SeverityHigh,
		Status:      domain.AlertOpen,
		CreatedAt:   time.Now().UTC(),
		Evidence:    accessLogMetadata(session),
	}
	go func() {
		_ = a.webhook.ExportAlerts([]domain.Alert{alert})
	}()
}

// accessLogSilent reports whether the shipper has stopped reporting. Silence is
// itself a signal: a reconciler that reacts only to what arrives can be
// defeated by arranging for nothing to arrive.
func (a *App) accessLogSilent() bool {
	last, _, _ := a.accessLog.snapshot()
	if last.IsZero() {
		// Never heard from. That is not "silent" — it is not configured, and
		// claiming an alarm for an unconfigured deployment would train the
		// operator to ignore it.
		return false
	}
	return time.Since(last) > accessLogSilenceAfter
}

const (
	applicationName       = "promtact"
	accessLogSilenceAfter = 15 * time.Minute
)
