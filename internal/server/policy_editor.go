package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The policy, where the person it governs can see it.
//
// It was a JSON file on the server that only root could edit. Which tools an
// agent may call is the central setting of this product, and a customer could
// neither read it nor change it — every adjustment meant asking us. A control
// nobody can configure is a control nobody owns.
//
// Only the parts a customer decides are exposed. Users, key hashes and SSO
// configuration stay out: they are account plumbing rather than policy, and
// sending key hashes to a browser is a poor idea whatever the role of the
// person holding it.

type policyView struct {
	ApprovedTools       []string          `json:"approved_tools"`
	ApprovedEgressHosts []string          `json:"approved_egress_hosts"`
	AgentIdentities     []policyAgentView `json:"agent_identities"`
	CorrelationWindow   string            `json:"correlation_window,omitempty"`
	Editable            bool              `json:"editable"`
	Path                string            `json:"path,omitempty"`
	Note                string            `json:"note,omitempty"`
}

type policyAgentView struct {
	AgentID string `json:"agent_id"`
	// The hash is never sent out. Whether one is set is all a reader needs, and
	// it is all this will say.
	HasKey bool `json:"has_key"`
}

func (a *App) handlePolicyDocument(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.readPolicyDocument(w, r)
	case http.MethodPut:
		a.writePolicyDocument(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (a *App) readPolicyDocument(w http.ResponseWriter, r *http.Request) {
	document, err := a.loadPolicyDocument()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	view := policyView{
		ApprovedTools:       stringsFromDocument(document, "approved_tools"),
		ApprovedEgressHosts: stringsFromDocument(document, "approved_egress_hosts"),
		CorrelationWindow:   stringFromDocument(document, "correlation_window"),
		Editable:            a.policyPath != "",
		Path:                a.policyPath,
	}
	if identities, ok := document["agent_identities"].([]any); ok {
		for _, entry := range identities {
			record, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			id, _ := record["agent_id"].(string)
			hash, _ := record["key_sha256"].(string)
			view.AgentIdentities = append(view.AgentIdentities, policyAgentView{
				AgentID: id,
				HasKey:  strings.TrimSpace(hash) != "",
			})
		}
	}
	if !view.Editable {
		view.Note = "This deployment was started without a policy file, so the policy shown is the built-in default and cannot be edited here."
	}
	writeJSON(w, http.StatusOK, view)
}

type policyUpdate struct {
	ApprovedTools       []string `json:"approved_tools"`
	ApprovedEgressHosts []string `json:"approved_egress_hosts"`
}

func (a *App) writePolicyDocument(w http.ResponseWriter, r *http.Request) {
	principal := principalFromRequest(r)
	if a.policyPath == "" {
		writeError(w, http.StatusBadRequest,
			errors.New("this deployment was started without a policy file, so there is nothing to write"))
		return
	}

	var update policyUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	document, err := a.loadPolicyDocument()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	before := stringsFromDocument(document, "approved_tools")
	tools := cleanPolicyList(update.ApprovedTools)
	hosts := cleanPolicyList(update.ApprovedEgressHosts)
	if len(tools) == 0 {
		// An empty list falls back to the built-in defaults rather than
		// approving nothing, which is not what somebody clearing the field
		// expects. Refusing is clearer than silently doing something else.
		writeError(w, http.StatusBadRequest,
			errors.New("at least one approved tool is required: an empty list falls back to the built-in defaults rather than approving nothing"))
		return
	}

	// Only these two keys are replaced. Everything else in the file — users,
	// key hashes, SSO, agent identities — is carried through untouched, because
	// this endpoint has no business rewriting configuration it does not show.
	document["approved_tools"] = toAnySlice(tools)
	document["approved_egress_hosts"] = toAnySlice(hosts)

	if err := a.savePolicyDocument(document); err != nil {
		a.recordAudit(r, principal, "policy.update", "policy", a.policyPath, "failed",
			map[string]string{"error": err.Error()})
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := a.ReloadPolicy(); err != nil {
		a.recordAudit(r, principal, "policy.update", "policy", a.policyPath, "written_not_loaded",
			map[string]string{"error": err.Error()})
		writeError(w, http.StatusInternalServerError,
			fmt.Errorf("the policy was written but could not be loaded: %w", err))
		return
	}

	// What changed, in the record. "Policy updated" tells an auditor nothing;
	// which tools were added or removed, and by whom, is the answer to the
	// question they are actually asking.
	a.recordAudit(r, principal, "policy.update", "policy", a.policyPath, "applied", map[string]string{
		"approved_tools_before": strings.Join(before, ","),
		"approved_tools_after":  strings.Join(tools, ","),
		"added":                 strings.Join(missingFrom(tools, before), ","),
		"removed":               strings.Join(missingFrom(before, tools), ","),
		"approved_egress_hosts": strings.Join(hosts, ","),
	})

	a.readPolicyDocument(w, r)
}

func (a *App) loadPolicyDocument() (map[string]any, error) {
	if a.policyPath == "" {
		return map[string]any{}, nil
	}
	raw, err := os.ReadFile(a.policyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	document := map[string]any{}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("the policy file is not valid JSON: %w", err)
	}
	return document, nil
}

// savePolicyDocument writes through a temporary file and a rename.
//
// A policy half-written because the disk filled is a gateway that will not
// start, and the moment it happens is the moment somebody is editing under
// pressure. The previous version is kept beside it for the same reason.
func (a *App) savePolicyDocument(document map[string]any) error {
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	if existing, err := os.ReadFile(a.policyPath); err == nil {
		backup := fmt.Sprintf("%s.bak-%s", a.policyPath, time.Now().UTC().Format("20060102-150405"))
		_ = os.WriteFile(backup, existing, 0o600)
	}

	directory := filepath.Dir(a.policyPath)
	temporary := filepath.Join(directory, ".policy.json.tmp")
	if err := os.WriteFile(temporary, encoded, 0o640); err != nil {
		return err
	}
	return os.Rename(temporary, a.policyPath)
}

func stringsFromDocument(document map[string]any, key string) []string {
	values, ok := document[key].([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, text)
		}
	}
	return out
}

func stringFromDocument(document map[string]any, key string) string {
	text, _ := document[key].(string)
	return text
}

func cleanPolicyList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		lowered := strings.ToLower(value)
		if _, exists := seen[lowered]; exists {
			continue
		}
		seen[lowered] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func toAnySlice(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func missingFrom(candidates []string, present []string) []string {
	index := map[string]struct{}{}
	for _, value := range present {
		index[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	var out []string
	for _, value := range candidates {
		if _, exists := index[strings.ToLower(strings.TrimSpace(value))]; !exists {
			out = append(out, value)
		}
	}
	return out
}
