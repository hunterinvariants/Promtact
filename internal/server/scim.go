package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/store"
)

// SCIM 2.0 (RFC 7643/7644) so an identity provider can provision and, more
// importantly, deprovision directory accounts.
//
// Three decisions are worth stating because they are where SCIM implementations
// usually go wrong.
//
// The endpoints live under /api/ rather than at the conventional /scim/v2 root.
// The authentication middleware only guards /api/ and /metrics, so a top-level
// mount would have been served entirely unauthenticated — a provisioning API
// open to the internet. Identity providers take an arbitrary base URL, so this
// costs nothing.
//
// The tenant is taken from the calling credential and never from the request
// body. An IdP token belongs to one customer; letting a payload name a tenant
// would turn provisioning into cross-tenant account creation.
//
// Deprovisioning suspends and revokes rather than deleting. An account that
// vanishes takes its audit trail with it, and the security requirement is that
// the credentials stop working — which suspension achieves, atomically.

const (
	scimUserSchema  = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimListSchema  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimErrorSchema = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimPatchSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimContentType = "application/scim+json"
	scimBasePath    = "/api/scim/v2/"
)

type scimName struct {
	Formatted string `json:"formatted,omitempty"`
}

type scimMeta struct {
	ResourceType string `json:"resourceType"`
	Created      string `json:"created,omitempty"`
	Location     string `json:"location,omitempty"`
}

type scimUser struct {
	Schemas    []string  `json:"schemas"`
	ID         string    `json:"id"`
	UserName   string    `json:"userName"`
	Name       *scimName `json:"name,omitempty"`
	Active     bool      `json:"active"`
	Roles      []string  `json:"roles,omitempty"`
	ExternalID string    `json:"externalId,omitempty"`
	Meta       scimMeta  `json:"meta"`
}

func scimUserFrom(user store.TenantUser) scimUser {
	return scimUser{
		Schemas:  []string{scimUserSchema},
		ID:       user.ID,
		UserName: user.Name,
		Active:   user.Status == store.StatusActive,
		Roles:    user.Roles,
		Meta: scimMeta{
			ResourceType: "User",
			Created:      user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Location:     scimBasePath + "Users/" + user.ID,
		},
	}
}

func writeSCIM(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", scimContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeSCIMError uses the SCIM error schema. An IdP parses these to decide
// whether to retry or to surface the problem to an administrator, so returning
// this service's generic error shape would make failures opaque at the far end.
func writeSCIMError(w http.ResponseWriter, status int, scimType string, detail string) {
	payload := map[string]any{
		"schemas": []string{scimErrorSchema},
		"status":  strconv.Itoa(status),
		"detail":  detail,
	}
	if scimType != "" {
		payload["scimType"] = scimType
	}
	writeSCIM(w, status, payload)
}

// handleSCIMServiceProviderConfig advertises exactly what is implemented.
// Claiming unsupported capabilities makes an IdP send operations that then fail
// silently, which is how deprovisioning quietly stops working.
func (a *App) handleSCIMServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeSCIMError(w, http.StatusMethodNotAllowed, "", "only GET is supported here")
		return
	}
	writeSCIM(w, http.StatusOK, map[string]any{
		"schemas":          []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
		"documentationUri": "https://github.com/hunterinvariants/promtact",
		"patch":            map[string]any{"supported": true},
		"bulk":             map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":           map[string]any{"supported": true, "maxResults": scimMaxResults},
		"changePassword":   map[string]any{"supported": false},
		"sort":             map[string]any{"supported": false},
		"etag":             map[string]any{"supported": false},
		"authenticationSchemes": []map[string]any{{
			"type":        "oauthbearertoken",
			"name":        "Bearer token",
			"description": "A tenant-scoped API key presented as a bearer token.",
			"primary":     true,
		}},
		"meta": map[string]any{"resourceType": "ServiceProviderConfig"},
	})
}

const scimMaxResults = 200

// handleSCIMUsers serves the collection: listing (with the one filter form an
// IdP actually needs) and creation.
func (a *App) handleSCIMUsers(w http.ResponseWriter, r *http.Request) {
	principal := principalFromRequest(r)
	tenant, ok := a.scimTenant(w, principal)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		users, err := a.store.ListTenantUsers(r.Context(), tenant)
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
			return
		}
		if filter := strings.TrimSpace(r.URL.Query().Get("filter")); filter != "" {
			wanted, ok := parseSCIMUserNameFilter(filter)
			if !ok {
				// Only the equality filter on userName is supported. A general
				// filter language would be a parser accepting attacker-supplied
				// input in front of the directory, for no provisioning benefit.
				writeSCIMError(w, http.StatusBadRequest, "invalidFilter",
					`only filters of the form: userName eq "value" are supported`)
				return
			}
			matched := users[:0]
			for _, user := range users {
				if strings.EqualFold(user.Name, wanted) {
					matched = append(matched, user)
				}
			}
			users = matched
		}
		if len(users) > scimMaxResults {
			users = users[:scimMaxResults]
		}

		resources := make([]scimUser, 0, len(users))
		for _, user := range users {
			resources = append(resources, scimUserFrom(user))
		}
		writeSCIM(w, http.StatusOK, map[string]any{
			"schemas":      []string{scimListSchema},
			"totalResults": len(resources),
			"itemsPerPage": len(resources),
			"startIndex":   1,
			"Resources":    resources,
		})

	case http.MethodPost:
		var req scimUser
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeSCIMError(w, http.StatusBadRequest, "invalidSyntax", err.Error())
			return
		}
		userName := strings.TrimSpace(req.UserName)
		if userName == "" {
			writeSCIMError(w, http.StatusBadRequest, "invalidValue", "userName is required")
			return
		}

		// Names are globally unique, so a collision must be reported as such
		// rather than as a generic failure the IdP will retry forever.
		if _, exists, err := a.store.UserByName(r.Context(), userName); err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
			return
		} else if exists {
			writeSCIMError(w, http.StatusConflict, "uniqueness", "a user with this userName already exists")
			return
		}

		created, err := a.store.CreateTenantUser(r.Context(), store.TenantUser{
			Tenant: tenant,
			Name:   userName,
			Roles:  scimRoles(req.Roles),
			Kind:   store.KindHuman,
			Status: scimStatus(req.Active),
		})
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
			return
		}
		a.recordAudit(r, principal, "scim.user.create", "user", created.ID, "accepted", map[string]string{
			"tenant":   tenant,
			"username": created.Name,
		})
		w.Header().Set("Location", scimBasePath+"Users/"+created.ID)
		writeSCIM(w, http.StatusCreated, scimUserFrom(created))

	default:
		writeSCIMError(w, http.StatusMethodNotAllowed, "", "unsupported method")
	}
}

// handleSCIMUser serves a single resource: read, replace, patch and delete.
func (a *App) handleSCIMUser(w http.ResponseWriter, r *http.Request) {
	principal := principalFromRequest(r)
	tenant, ok := a.scimTenant(w, principal)
	if !ok {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, scimBasePath+"Users/")
	if id == "" || strings.Contains(id, "/") {
		writeSCIMError(w, http.StatusNotFound, "", "unknown resource")
		return
	}

	// The tenant is part of the lookup, so another customer's id resolves to
	// "not found" rather than leaking that it exists.
	user, found, err := a.store.UserByID(r.Context(), tenant, id)
	if err != nil {
		writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
		return
	}
	if !found {
		writeSCIMError(w, http.StatusNotFound, "", "user not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeSCIM(w, http.StatusOK, scimUserFrom(user))

	case http.MethodDelete:
		// Deletion means "revoke access", not "erase the record": an account
		// that disappears takes its audit trail with it.
		if err := a.store.DeactivateUser(r.Context(), user.ID); err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
			return
		}
		a.recordAudit(r, principal, "scim.user.deprovision", "user", user.ID, "accepted", map[string]string{
			"tenant": tenant, "username": user.Name, "method": "delete",
		})
		w.WriteHeader(http.StatusNoContent)

	case http.MethodPut, http.MethodPatch:
		active, roles, err := a.scimDesiredState(r, user)
		if err != nil {
			writeSCIMError(w, http.StatusBadRequest, "invalidValue", err.Error())
			return
		}
		if roles != nil {
			if err := a.store.SetUserRoles(r.Context(), tenant, user.ID, roles); err != nil {
				writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
				return
			}
		}
		if wasActive := user.Status == store.StatusActive; active != wasActive {
			apply := a.store.ReactivateUser
			outcome := "reactivated"
			if !active {
				apply = a.store.DeactivateUser
				outcome = "deprovisioned"
			}
			if err := apply(r.Context(), user.ID); err != nil {
				writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
				return
			}
			a.recordAudit(r, principal, "scim.user."+outcome, "user", user.ID, "accepted", map[string]string{
				"tenant": tenant, "username": user.Name, "method": strings.ToLower(r.Method),
			})
		}

		updated, _, err := a.store.UserByID(r.Context(), tenant, user.ID)
		if err != nil {
			writeSCIMError(w, http.StatusInternalServerError, "", err.Error())
			return
		}
		writeSCIM(w, http.StatusOK, scimUserFrom(updated))

	default:
		writeSCIMError(w, http.StatusMethodNotAllowed, "", "unsupported method")
	}
}

// scimDesiredState reads the intended active flag and roles from either a PUT
// body or a PATCH operation list. Unset values keep what the record already has.
func (a *App) scimDesiredState(r *http.Request, user store.TenantUser) (bool, []string, error) {
	active := user.Status == store.StatusActive

	if r.Method == http.MethodPut {
		var req scimUser
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return active, nil, err
		}
		var roles []string
		if len(req.Roles) > 0 {
			roles = scimRoles(req.Roles)
		}
		return req.Active, roles, nil
	}

	var patch struct {
		Schemas    []string `json:"schemas"`
		Operations []struct {
			Op    string          `json:"op"`
			Path  string          `json:"path"`
			Value json.RawMessage `json:"value"`
		} `json:"Operations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		return active, nil, err
	}

	var roles []string
	for _, op := range patch.Operations {
		switch {
		case strings.EqualFold(op.Op, "remove") && strings.EqualFold(op.Path, "active"):
			active = false
		case strings.EqualFold(op.Path, "active"):
			// Entra sends the boolean as a string often enough that refusing it
			// would break deprovisioning against a real IdP.
			var asBool bool
			if err := json.Unmarshal(op.Value, &asBool); err == nil {
				active = asBool
				continue
			}
			var asString string
			if err := json.Unmarshal(op.Value, &asString); err == nil {
				parsed, err := strconv.ParseBool(strings.TrimSpace(asString))
				if err != nil {
					return active, nil, errors.New("active must be a boolean")
				}
				active = parsed
				continue
			}
			return active, nil, errors.New("active must be a boolean")
		case strings.EqualFold(op.Path, "roles"):
			var values []string
			if err := json.Unmarshal(op.Value, &values); err == nil {
				roles = scimRoles(values)
			}
		case op.Path == "":
			// A pathless patch carries the changed attributes as an object.
			var body scimUser
			if err := json.Unmarshal(op.Value, &body); err == nil {
				active = body.Active
				if len(body.Roles) > 0 {
					roles = scimRoles(body.Roles)
				}
			}
		}
	}
	return active, roles, nil
}

// scimTenant takes the tenant from the calling credential. It is never read
// from the request body: an IdP token belongs to one customer, and honouring a
// tenant in the payload would make provisioning a cross-tenant primitive.
func (a *App) scimTenant(w http.ResponseWriter, principal auth.Principal) (string, bool) {
	if !a.store.HasDirectory() {
		writeSCIMError(w, http.StatusNotImplemented, "", "SCIM provisioning requires the tenant directory")
		return "", false
	}
	tenant := strings.TrimSpace(principal.Tenant)
	if tenant == "" {
		writeSCIMError(w, http.StatusForbidden, "", "the calling credential is not bound to a tenant")
		return "", false
	}
	return tenant, true
}

// scimRoles passes IdP-supplied roles through the application's allowlist. Role
// and group claims are attacker-influenceable, so an unmapped value must grant
// nothing rather than being stored verbatim.
func scimRoles(roles []string) []string {
	allowed := make([]string, 0, len(roles))
	for _, role := range roles {
		switch strings.ToLower(strings.TrimSpace(role)) {
		case auth.RoleViewer, auth.RoleIngestor, auth.RoleAnalyst, auth.RoleOperator, auth.RoleAdmin:
			allowed = append(allowed, strings.ToLower(strings.TrimSpace(role)))
		}
	}
	if len(allowed) == 0 {
		return []string{auth.RoleViewer}
	}
	return allowed
}

func scimStatus(active bool) string {
	if active {
		return store.StatusActive
	}
	return store.StatusSuspended
}

// parseSCIMUserNameFilter accepts only: userName eq "value".
func parseSCIMUserNameFilter(filter string) (string, bool) {
	fields := strings.SplitN(strings.TrimSpace(filter), " ", 3)
	if len(fields) != 3 {
		return "", false
	}
	if !strings.EqualFold(fields[0], "userName") || !strings.EqualFold(fields[1], "eq") {
		return "", false
	}
	value := strings.TrimSpace(fields[2])
	if len(value) < 2 || !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) {
		return "", false
	}
	inner := value[1 : len(value)-1]
	// The quotes must delimit one value and nothing else. Checking only the
	// first and last character would accept a compound expression such as
	//   userName eq "a" and roles eq "admin"
	// and silently treat the whole tail as the name being searched for.
	if strings.Contains(inner, `"`) {
		return "", false
	}
	return inner, true
}
