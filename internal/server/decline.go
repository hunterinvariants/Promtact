package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/domain"
)

// Declining a held call.
//
// The queue could only be approved out of, and approving executes. So the
// person a call was held for had exactly one available answer, and every held
// call was either eventually performed or left in the queue forever. A control
// that stops an action to ask a human, and then gives that human no way to say
// no, is not asking a question - it is delaying a yes.
//
// It is also the first thing a buyer looks for in an approval queue, and the
// answer "you can only approve" is not one worth giving.
//
// The decline is recorded in the audit chain with its reason, because "a person
// refused this, here is who and why" is evidence, and it is the half of the
// record that was missing entirely.

func (a *App) handleResponseDecline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	principal := principalFromRequest(r)
	// The same authority as approving: deciding not to act is as much a
	// decision as deciding to act, and a queue where anyone can silently
	// discard held calls is worse than one nobody can clear.
	if !principal.HasAny(auth.RoleAdmin, auth.RoleOperator) {
		writeError(w, http.StatusForbidden, errors.New("operator role required"))
		return
	}

	var req struct {
		ActionID   string `json:"action_id"`
		DeclinedBy string `json:"declined_by"`
		Reason     string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.ActionID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("action_id is required"))
		return
	}
	if strings.TrimSpace(req.DeclinedBy) == "" {
		req.DeclinedBy = principal.Name
	}
	if strings.TrimSpace(req.DeclinedBy) == "" {
		req.DeclinedBy = "operator"
	}

	tenant := tenantForPrincipal(principal)
	action, ok, err := a.declineActionForTenant(req.ActionID, req.DeclinedBy, req.Reason, time.Now().UTC(), tenant)
	if err != nil {
		// Already approved is a conflict between two people's decisions, not a
		// server fault, and the caller needs to know which it was.
		if strings.Contains(err.Error(), "already approved") {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok || !sameTenant(action.Tenant, tenant) {
		writeError(w, http.StatusNotFound, errors.New("action not found"))
		return
	}

	a.recordAudit(r, principal, "responses.decline", "response_action", action.ID, "declined", map[string]string{
		"tool":        action.Metadata["tool"],
		"declined_by": req.DeclinedBy,
		"reason":      strings.TrimSpace(req.Reason),
	})
	writeJSON(w, http.StatusOK, action)
}

func (a *App) declineActionForTenant(id string, declinedBy string, reason string, at time.Time, tenant string) (domain.ResponseAction, bool, error) {
	if a.tenantRegistry == nil || !a.tenantRegistry.physicalMode {
		return a.store.DeclineAction(id, declinedBy, reason, at)
	}
	st, err := a.storeForTenant(tenant)
	if err != nil {
		return domain.ResponseAction{}, false, err
	}
	return st.DeclineAction(id, declinedBy, reason, at)
}
