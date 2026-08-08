package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/domain"
	"github.com/hunterinvariants/promtact/internal/store"
)

// The credential broker.
//
// An agent that holds the API key for the tool it calls can reach that tool
// without this gateway, and during testing exactly that happened: an assistant
// whose gated call was slow read the files directly instead, and nothing was
// gated - not because the gateway failed, but because it was never involved.
//
// Holding the credential here removes the option rather than discouraging it.
// The agent's token is accepted by this gateway and by nothing else, so a route
// around the gateway arrives at the tool unauthenticated and is refused there.
//
// This needs no cooperation from the agent and no network controls, which is
// why it comes before egress rules and workload attestation: those require the
// customer's platform to agree, and this does not.

// upstreamAuth is the authorisation the gateway presents upstream. It is a type
// rather than a bare string so that a brokered credential and the statically
// configured token cannot be confused at a call site, and so the header is
// carried with the value that belongs in it.
type upstreamAuth struct {
	Header string
	Value  string

	// CredentialID identifies which brokered credential was used, for the audit
	// record. The secret itself never travels with this beyond the request.
	CredentialID string
	Fingerprint  string
}

func (u upstreamAuth) empty() bool { return strings.TrimSpace(u.Value) == "" }

// staticUpstreamAuth is the pre-brokering behaviour: one token for the whole
// upstream, configured at startup. Deployments that have not adopted brokering
// keep working exactly as before.
func (a *App) staticUpstreamAuth() upstreamAuth {
	token := a.gatewayMCPUpstreamToken()
	if strings.TrimSpace(token) == "" {
		return upstreamAuth{}
	}
	return upstreamAuth{Header: "Authorization", Value: "Bearer " + token}
}

// brokerUpstreamAuth resolves the credential to present for a tool call,
// falling back to the statically configured token when the tenant has no
// credential that applies.
//
// A failure to load or unseal is deliberately not fatal to the call: it falls
// back, and reports the reason to the caller for the audit record. Refusing
// every tool call in the deployment because one credential row cannot be
// decrypted would turn a configuration problem into an outage.
func (a *App) brokerUpstreamAuth(tenant string, toolName string) (upstreamAuth, string) {
	if a.store == nil {
		return a.staticUpstreamAuth(), ""
	}
	credentials, err := a.store.CredentialsWithSecrets(tenant)
	if err != nil {
		return a.staticUpstreamAuth(), "credential lookup failed: " + err.Error()
	}
	if len(credentials) == 0 {
		return a.staticUpstreamAuth(), ""
	}
	selected, ok := domain.SelectCredential(credentials, tenant, toolName)
	if !ok {
		return a.staticUpstreamAuth(), ""
	}
	a.store.MarkCredentialUsed(tenant, selected.ID, time.Now().UTC())
	return upstreamAuth{
		Header:       selected.HeaderName(),
		Value:        selected.HeaderValue(),
		CredentialID: selected.ID,
		Fingerprint:  selected.Fingerprint,
	}, ""
}

// handleCredentials manages brokered tool credentials. Admin only, and there is
// no route that returns a secret: a credential can be written, listed by
// fingerprint, and deleted, but never read back. An endpoint that returned the
// plaintext would recreate the problem this exists to remove, since anyone able
// to call it could then hold the credential themselves.
func (a *App) handleCredentials(w http.ResponseWriter, r *http.Request) {
	principal := principalFromRequest(r)
	if !principal.HasAny(auth.RoleAdmin) {
		writeError(w, http.StatusForbidden, errors.New("admin role required"))
		return
	}
	tenant := tenantForPrincipal(principal)

	switch r.Method {
	case http.MethodGet:
		credentials, err := a.store.Credentials(tenant)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if credentials == nil {
			credentials = []domain.Credential{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"credentials": credentials,
			// So the console can explain the fallback rather than implying
			// every call is brokered when some are not.
			"static_upstream_token": !a.staticUpstreamAuth().empty(),
		})

	case http.MethodPost, http.MethodPut:
		var req struct {
			ID          string `json:"id"`
			Tool        string `json:"tool"`
			Header      string `json:"header"`
			Scheme      string `json:"scheme"`
			Secret      string `json:"secret"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(req.Secret) == "" {
			writeError(w, http.StatusBadRequest, errors.New("secret is required"))
			return
		}
		if strings.TrimSpace(req.Tool) == "" {
			writeError(w, http.StatusBadRequest, errors.New(
				`tool is required: an exact tool name, a "prefix_*" wildcard, or "*" for every tool`))
			return
		}
		id := strings.TrimSpace(req.ID)
		if id == "" {
			id = newCredentialID()
		}
		stored, err := a.store.SaveCredential(domain.Credential{
			ID:          id,
			Tenant:      tenant,
			Tool:        req.Tool,
			Header:      strings.TrimSpace(req.Header),
			Scheme:      strings.TrimSpace(req.Scheme),
			Description: strings.TrimSpace(req.Description),
		}, req.Secret)
		if err != nil {
			// A missing encryption key is a configuration answer, not a server
			// fault, and saying so is more useful than a 500.
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrCredentialSealRequired()) {
				status = http.StatusPreconditionFailed
			}
			writeError(w, status, err)
			return
		}
		a.recordCredentialChange(r, principal, stored, "stored")
		writeJSON(w, http.StatusOK, stored)

	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeError(w, http.StatusBadRequest, errors.New("id is required"))
			return
		}
		removed, err := a.store.DeleteCredential(tenant, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if !removed {
			writeError(w, http.StatusNotFound, errors.New("no such credential"))
			return
		}
		a.recordCredentialChange(r, principal, domain.Credential{ID: id, Tenant: tenant}, "removed")
		writeJSON(w, http.StatusOK, map[string]any{"removed": id})

	default:
		methodNotAllowed(w)
	}
}

// recordCredentialChange writes the administrative action to the audit chain.
// The fingerprint is recorded and the secret is not, which is the whole point
// of having a fingerprint: a reader can tell that the credential changed, and
// which one it is, without the audit record becoming a place secrets live.
func (a *App) recordCredentialChange(r *http.Request, principal auth.Principal, credential domain.Credential, outcome string) {
	metadata := map[string]string{"tool": credential.Tool}
	if credential.Fingerprint != "" {
		metadata["fingerprint"] = credential.Fingerprint
	}
	a.recordAudit(r, principal, "tool_credential", "credential", credential.ID, outcome, metadata)
}

func newCredentialID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "cred-" + time.Now().UTC().Format("20060102150405")
	}
	return "cred-" + hex.EncodeToString(buf)
}
