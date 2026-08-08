package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
)

// Rate-limiting the audit record written for a rejected request.
//
// Every unauthenticated call to /api/ appended a record to the hash chain,
// unconditionally. On the live deployment that turned out to be 617 of roughly
// 900 records - more than two thirds of the evidence chain was one poller
// failing to authenticate every ten seconds. The trail that is supposed to
// answer "what did the agent do, and who let it" was mostly noise, and the
// noise arrived faster than the decisions.
//
// It is also a way in. Anyone who can reach the endpoint could grow the audit
// chain without limit, without credentials: filling the database, pushing real
// decisions past the retention window, and inflating the very record the
// product sells. A control that an anonymous caller can drive is not one.
//
// So repeated failures from the same source, on the same path, become one
// record per window carrying a count. The evidence that somebody was probing
// survives - arguably in a more useful form, since a count is the thing an
// investigator wants - while the chain stops being writable by strangers.

const (
	authFailureWindow = 5 * time.Minute
	// A bound on the tracking map itself. Without it, an attacker varying the
	// source address would move the unbounded growth from the audit chain into
	// memory, which is the same bug wearing a hat.
	authFailureSources = 4096
)

type authFailureState struct {
	firstSeen  time.Time
	suppressed int
}

// noteAuthFailure reports whether this rejection should be recorded, and how
// many were suppressed since the last one that was.
func (a *App) noteAuthFailure(key string, now time.Time) (bool, int) {
	a.authFailureMu.Lock()
	defer a.authFailureMu.Unlock()

	if a.authFailures == nil {
		a.authFailures = make(map[string]*authFailureState)
	}
	state, ok := a.authFailures[key]
	if ok && now.Sub(state.firstSeen) < authFailureWindow {
		state.suppressed++
		return false, 0
	}

	suppressed := 0
	if ok {
		suppressed = state.suppressed
	}
	if !ok && len(a.authFailures) >= authFailureSources {
		// Full: drop the oldest window rather than refusing to track, so a
		// flood of new sources cannot stop real ones being recorded. Scanning
		// is acceptable at this size and only happens when the map is full.
		var oldestKey string
		var oldest time.Time
		for candidate, entry := range a.authFailures {
			if oldest.IsZero() || entry.firstSeen.Before(oldest) {
				oldestKey, oldest = candidate, entry.firstSeen
			}
		}
		delete(a.authFailures, oldestKey)
	}
	a.authFailures[key] = &authFailureState{firstSeen: now}
	return true, suppressed
}

// recordAuthFailure writes at most one record per source, path and window.
func (a *App) recordAuthFailure(r *http.Request, principal auth.Principal, action string) {
	key := a.sourceIP(r) + "|" + r.Method + "|" + r.URL.Path
	record, suppressed := a.noteAuthFailure(key, time.Now().UTC())
	if !record {
		return
	}
	metadata := map[string]string{"method": r.Method}
	if suppressed > 0 {
		// The count is the point: one rejection is noise, four hundred in five
		// minutes is a finding, and only the aggregate can tell them apart.
		metadata["suppressed_since_last"] = fmt.Sprintf("%d", suppressed)
		metadata["window"] = authFailureWindow.String()
	}
	a.recordAudit(r, principal, action, "http_request", r.URL.Path, "denied", metadata)
}
