package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/store"
)

// Platform provisioning API. Creating, suspending and keying customer tenants is
// restricted to the platform operator (an admin on the "default" tenant); a
// customer's own admin can administer their tenant elsewhere but must never be
// able to create or read other tenants.

type createTenantRequest struct {
	Tenant      string `json:"tenant"`
	DisplayName string `json:"display_name"`
	Plan        string `json:"plan"`
	AdminName   string `json:"admin_name"`
}

type createUserRequest struct {
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

type createKeyRequest struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type tenantStatusRequest struct {
	Status string `json:"status"`
}

// newAPIKeySecret returns a fresh 256-bit key. The plaintext is shown to the
// operator once and never persisted; only its SHA-256 is stored.
func newAPIKeySecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "promtact_" + hex.EncodeToString(buf), nil
}

func (a *App) requirePlatformAdmin(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	principal := principalFromRequest(r)
	if !isPlatformAdmin(principal) {
		a.recordAudit(r, principal, "admin.authorize", "tenant_account", r.URL.Path, "denied", map[string]string{
			"method": r.Method,
		})
		writeError(w, http.StatusForbidden, errors.New("platform administrator role required"))
		return auth.Principal{}, false
	}
	if !a.store.HasDirectory() {
		writeError(w, http.StatusServiceUnavailable, errors.New("tenant provisioning requires a postgres backend"))
		return auth.Principal{}, false
	}
	return principal, true
}

// handleAdminTenants serves /api/admin/tenants (list and create).
func (a *App) handleAdminTenants(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.requirePlatformAdmin(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		accounts, err := a.store.ListTenantAccounts(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, accounts)
	case http.MethodPost:
		var req createTenantRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		tenant := strings.ToLower(strings.TrimSpace(req.Tenant))
		if tenant == "" {
			writeError(w, http.StatusBadRequest, errors.New("tenant is required"))
			return
		}
		adminName := strings.TrimSpace(req.AdminName)
		if adminName == "" {
			adminName = tenant + "-admin"
		}

		account, err := a.store.CreateTenantAccount(r.Context(), store.TenantAccount{
			Tenant:      tenant,
			DisplayName: strings.TrimSpace(req.DisplayName),
			Plan:        strings.TrimSpace(req.Plan),
		})
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		user, secret, key, err := a.provisionUserWithKey(r, tenant, adminName, []string{auth.RoleAdmin}, "initial admin key")
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}

		a.recordAudit(r, principal, "admin.tenant.create", "tenant_account", tenant, "created", map[string]string{
			"admin_user": user.Name,
			"key_id":     key.ID,
		})
		writeJSON(w, http.StatusCreated, map[string]any{
			"tenant":  account,
			"user":    user,
			"key":     key,
			"api_key": secret,
			"notice":  "store this api_key now: it is shown once and cannot be recovered",
		})
	default:
		methodNotAllowed(w)
	}
}

// provisionUserWithKey creates a user plus a first API key, returning the
// plaintext secret for one-time display.
func (a *App) provisionUserWithKey(r *http.Request, tenant string, name string, roles []string, keyName string) (store.TenantUser, string, store.APIKey, error) {
	user, err := a.store.CreateTenantUser(r.Context(), store.TenantUser{
		Tenant: tenant,
		Name:   name,
		Roles:  roles,
	})
	if err != nil {
		return store.TenantUser{}, "", store.APIKey{}, err
	}
	secret, err := newAPIKeySecret()
	if err != nil {
		return store.TenantUser{}, "", store.APIKey{}, err
	}
	key, err := a.store.CreateAPIKey(r.Context(), store.APIKey{
		Tenant: tenant,
		UserID: user.ID,
		Name:   keyName,
	}, auth.HashToken(secret))
	if err != nil {
		return store.TenantUser{}, "", store.APIKey{}, err
	}
	return user, secret, key, nil
}

// handleAdminTenantResource serves /api/admin/tenants/{tenant}/{resource}.
func (a *App) handleAdminTenantResource(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.requirePlatformAdmin(w, r)
	if !ok {
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/tenants/"), "/")
	if rest == "" {
		writeError(w, http.StatusBadRequest, errors.New("tenant is required"))
		return
	}
	parts := strings.Split(rest, "/")
	tenant := strings.ToLower(strings.TrimSpace(parts[0]))
	resource := ""
	if len(parts) > 1 {
		resource = parts[1]
	}

	switch {
	case resource == "usage":
		a.handleAdminTenantUsage(w, r, tenant)

	case resource == "status" && r.Method == http.MethodPost:
		var req tenantStatusRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.store.SetTenantAccountStatus(r.Context(), tenant, strings.TrimSpace(req.Status)); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		a.recordAudit(r, principal, "admin.tenant.status", "tenant_account", tenant, req.Status, nil)
		writeJSON(w, http.StatusOK, map[string]string{"tenant": tenant, "status": req.Status})

	case resource == "users" && r.Method == http.MethodGet:
		users, err := a.store.ListTenantUsers(r.Context(), tenant)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, users)

	case resource == "users" && r.Method == http.MethodPost:
		var req createUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusBadRequest, errors.New("name is required"))
			return
		}
		user, secret, key, err := a.provisionUserWithKey(r, tenant, strings.TrimSpace(req.Name), req.Roles, "initial key")
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		a.recordAudit(r, principal, "admin.user.create", "tenant_user", user.ID, "created", map[string]string{
			"tenant": tenant,
			"name":   user.Name,
		})
		writeJSON(w, http.StatusCreated, map[string]any{
			"user":    user,
			"key":     key,
			"api_key": secret,
			"notice":  "store this api_key now: it is shown once and cannot be recovered",
		})

	case resource == "keys" && r.Method == http.MethodGet:
		keys, err := a.store.ListAPIKeys(r.Context(), tenant)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, keys)

	case resource == "keys" && r.Method == http.MethodPost:
		var req createKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(req.UserID) == "" {
			writeError(w, http.StatusBadRequest, errors.New("user_id is required"))
			return
		}
		secret, err := newAPIKeySecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		key, err := a.store.CreateAPIKey(r.Context(), store.APIKey{
			Tenant: tenant,
			UserID: strings.TrimSpace(req.UserID),
			Name:   strings.TrimSpace(req.Name),
		}, auth.HashToken(secret))
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		a.recordAudit(r, principal, "admin.key.create", "api_key", key.ID, "created", map[string]string{
			"tenant":  tenant,
			"user_id": key.UserID,
		})
		writeJSON(w, http.StatusCreated, map[string]any{
			"key":     key,
			"api_key": secret,
			"notice":  "store this api_key now: it is shown once and cannot be recovered",
		})

	case resource == "keys" && r.Method == http.MethodDelete:
		if len(parts) < 3 {
			writeError(w, http.StatusBadRequest, errors.New("key id is required"))
			return
		}
		keyID := parts[2]
		if err := a.store.RevokeAPIKey(r.Context(), tenant, keyID); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		a.recordAudit(r, principal, "admin.key.revoke", "api_key", keyID, "revoked", map[string]string{
			"tenant": tenant,
		})
		writeJSON(w, http.StatusOK, map[string]string{"tenant": tenant, "key_id": keyID, "status": "revoked"})

	default:
		methodNotAllowed(w)
	}
}
