package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hunterinvariants/promtact/internal/auth"
)

// Registering an agent, without us.
//
// Until now this was `promtactl tenant add-agent` run by the vendor on the
// customer's server. That is not onboarding, it is a support ticket: a customer
// who wants a second agent has to ask, and a prospect evaluating the product
// cannot connect anything at all.
//
// The secret is generated here and returned exactly once. Letting a caller
// choose it invites a memorable one; storing it would mean a database that can
// impersonate every agent it governs. Only the hash is kept, which is the same
// arrangement as for user credentials and for the same reason.

type agentRegistration struct {
	AgentID string `json:"agent_id"`
}

type agentCreated struct {
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
	Note    string `json:"note"`
}

func (a *App) handleAgentRegistry(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		a.createAgentIdentity(w, r)
	case http.MethodDelete:
		a.deleteAgentIdentity(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (a *App) createAgentIdentity(w http.ResponseWriter, r *http.Request) {
	principal := principalFromRequest(r)
	if a.policyPath == "" {
		writeError(w, http.StatusBadRequest,
			errors.New("this deployment was started without a policy file, so an agent cannot be registered here"))
		return
	}

	var registration agentRegistration
	if err := json.NewDecoder(r.Body).Decode(&registration); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	agentID := strings.ToLower(strings.TrimSpace(registration.AgentID))
	if agentID == "" {
		writeError(w, http.StatusBadRequest, errors.New("an agent id is required"))
		return
	}
	if strings.ContainsAny(agentID, " \t\n/\\") {
		writeError(w, http.StatusBadRequest,
			errors.New("an agent id may not contain spaces or slashes: it appears in policy files and audit records"))
		return
	}

	document, err := a.loadPolicyDocument()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	identities, _ := document["agent_identities"].([]any)
	for _, entry := range identities {
		record, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if existing, _ := record["agent_id"].(string); strings.EqualFold(existing, agentID) {
			// Replacing silently would revoke the running agent's key without
			// anyone asking for it.
			writeError(w, http.StatusConflict,
				fmt.Errorf("an agent named %q is already registered; remove it first if you mean to replace its key", agentID))
			return
		}
	}

	secret, err := generateAgentSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	document["agent_identities"] = append(identities, map[string]any{
		"agent_id":   agentID,
		"key_sha256": auth.HashToken(secret),
	})
	if err := a.savePolicyDocument(document); err != nil {
		a.recordAudit(r, principal, "agent.register", "agent", agentID, "failed",
			map[string]string{"error": err.Error()})
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := a.ReloadPolicy(); err != nil {
		a.recordAudit(r, principal, "agent.register", "agent", agentID, "written_not_loaded",
			map[string]string{"error": err.Error()})
		writeError(w, http.StatusInternalServerError,
			fmt.Errorf("the agent was written but the policy could not be loaded: %w", err))
		return
	}

	// The secret is not in the record. The record says an agent was registered
	// and by whom, which is what it is for.
	a.recordAudit(r, principal, "agent.register", "agent", agentID, "registered", nil)

	writeJSON(w, http.StatusCreated, agentCreated{
		AgentID: agentID,
		Secret:  secret,
		Note:    "This secret is shown once and is not stored. Only its hash is kept, so it cannot be recovered — if it is lost, remove the agent and register it again.",
	})
}

func (a *App) deleteAgentIdentity(w http.ResponseWriter, r *http.Request) {
	principal := principalFromRequest(r)
	if a.policyPath == "" {
		writeError(w, http.StatusBadRequest, errors.New("this deployment was started without a policy file"))
		return
	}

	agentID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/policy/agents/"))
	agentID, err := url.PathUnescape(agentID)
	if err != nil || agentID == "" {
		writeError(w, http.StatusBadRequest, errors.New("an agent id is required"))
		return
	}

	document, err := a.loadPolicyDocument()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	identities, _ := document["agent_identities"].([]any)
	kept := make([]any, 0, len(identities))
	removed := false
	for _, entry := range identities {
		record, ok := entry.(map[string]any)
		if ok {
			if existing, _ := record["agent_id"].(string); strings.EqualFold(existing, agentID) {
				removed = true
				continue
			}
		}
		kept = append(kept, entry)
	}
	if !removed {
		writeError(w, http.StatusNotFound, fmt.Errorf("no agent named %q is registered", agentID))
		return
	}

	document["agent_identities"] = kept
	if err := a.savePolicyDocument(document); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := a.ReloadPolicy(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Removing an identity does not stop that agent: it stops it working
	// unattended, because its calls are held for a person from the next one on.
	// Saying so is the difference between an operator expecting silence and
	// expecting a queue.
	a.recordAudit(r, principal, "agent.remove", "agent", agentID, "removed", nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": agentID,
		"removed":  true,
		"note":     "Calls from this agent are now held for a person rather than refused. It is not blocked, it is unattended-no-longer.",
	})
}

// generateAgentSecret produces a key with enough entropy that guessing it is
// not a strategy, in a form somebody can paste into a configuration file.
func generateAgentSecret() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
