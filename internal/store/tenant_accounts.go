package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SaaS tenant directory: tenant accounts, their users, and API keys live in
// Postgres so customers can be provisioned live, without a server restart.
// Identity lookups query the database on demand rather than caching, so a
// revoked key or a suspended tenant takes effect immediately.

type TenantAccount struct {
	Tenant      string    `json:"tenant"`
	DisplayName string    `json:"display_name,omitempty"`
	Status      string    `json:"status"`
	Plan        string    `json:"plan"`
	CreatedAt   time.Time `json:"created_at"`
}

type TenantUser struct {
	ID        string    `json:"id"`
	Tenant    string    `json:"tenant"`
	Name      string    `json:"name"`
	Roles     []string  `json:"roles"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type APIKey struct {
	ID         string     `json:"id"`
	Tenant     string     `json:"tenant"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Identity is a principal resolved from the tenant directory. It is deliberately
// free of any auth-package types so the store stays decoupled from auth.
type Identity struct {
	Name   string
	Tenant string
	Roles  []string
}

const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

var errNoDirectory = errors.New("tenant directory requires a postgres backend")

// HasDirectory reports whether a database-backed tenant directory is available.
// Without one (file or memory mode) the server falls back to the users declared
// in policy.json, which keeps single-tenant self-hosted deployments working.
func (s *Store) HasDirectory() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db != nil
}

func (s *Store) directoryDB() (*sql.DB, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, errNoDirectory
	}
	return s.db, nil
}

func NewID(prefix string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return prefix + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf)
}

// CreateTenantAccount registers a new customer tenant.
func (s *Store) CreateTenantAccount(ctx context.Context, account TenantAccount) (TenantAccount, error) {
	db, err := s.directoryDB()
	if err != nil {
		return TenantAccount{}, err
	}
	account.Tenant = strings.ToLower(strings.TrimSpace(account.Tenant))
	if account.Tenant == "" {
		return TenantAccount{}, errors.New("tenant is required")
	}
	if account.Status == "" {
		account.Status = StatusActive
	}
	if account.Plan == "" {
		account.Plan = "standard"
	}
	account.CreatedAt = time.Now().UTC()

	_, err = db.ExecContext(ctx, `
INSERT INTO promtact_tenant_accounts (tenant, display_name, status, plan, created_at)
VALUES ($1, $2, $3, $4, $5)`,
		account.Tenant, account.DisplayName, account.Status, account.Plan, account.CreatedAt)
	if err != nil {
		return TenantAccount{}, err
	}
	return account, nil
}

func (s *Store) ListTenantAccounts(ctx context.Context) ([]TenantAccount, error) {
	db, err := s.directoryDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT tenant, display_name, status, plan, created_at
FROM promtact_tenant_accounts ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := make([]TenantAccount, 0, 8)
	for rows.Next() {
		var a TenantAccount
		if err := rows.Scan(&a.Tenant, &a.DisplayName, &a.Status, &a.Plan, &a.CreatedAt); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// SetTenantAccountStatus suspends or reactivates a tenant. A suspended tenant's
// principals stop authenticating immediately.
func (s *Store) SetTenantAccountStatus(ctx context.Context, tenant string, status string) error {
	db, err := s.directoryDB()
	if err != nil {
		return err
	}
	if status != StatusActive && status != StatusSuspended {
		return fmt.Errorf("invalid tenant status %q", status)
	}
	result, err := db.ExecContext(ctx,
		`UPDATE promtact_tenant_accounts SET status = $2 WHERE tenant = $1`,
		strings.ToLower(strings.TrimSpace(tenant)), status)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("tenant not found")
	}
	return nil
}

// CreateTenantUser adds a user to a tenant. Names are globally unique so a
// login name resolves to exactly one principal.
func (s *Store) CreateTenantUser(ctx context.Context, user TenantUser) (TenantUser, error) {
	db, err := s.directoryDB()
	if err != nil {
		return TenantUser{}, err
	}
	user.Tenant = strings.ToLower(strings.TrimSpace(user.Tenant))
	user.Name = strings.TrimSpace(user.Name)
	if user.Tenant == "" || user.Name == "" {
		return TenantUser{}, errors.New("tenant and name are required")
	}
	if user.ID == "" {
		user.ID = NewID("usr")
	}
	if user.Status == "" {
		user.Status = StatusActive
	}
	if len(user.Roles) == 0 {
		user.Roles = []string{"viewer"}
	}
	user.CreatedAt = time.Now().UTC()

	_, err = db.ExecContext(ctx, `
INSERT INTO promtact_tenant_users (id, tenant, name, roles, status, created_at)
VALUES ($1, $2, $3, $4, $5, $6)`,
		user.ID, user.Tenant, user.Name, encodeRoles(user.Roles), user.Status, user.CreatedAt)
	if err != nil {
		return TenantUser{}, err
	}
	return user, nil
}

func (s *Store) ListTenantUsers(ctx context.Context, tenant string) ([]TenantUser, error) {
	db, err := s.directoryDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT id, tenant, name, roles, status, created_at
FROM promtact_tenant_users WHERE tenant = $1 ORDER BY created_at ASC`,
		strings.ToLower(strings.TrimSpace(tenant)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]TenantUser, 0, 8)
	for rows.Next() {
		var u TenantUser
		var roles string
		if err := rows.Scan(&u.ID, &u.Tenant, &u.Name, &roles, &u.Status, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.Roles = decodeRoles(roles)
		users = append(users, u)
	}
	return users, rows.Err()
}

// CreateAPIKey stores only the SHA-256 of the key; the caller shows the
// plaintext to the operator once and then discards it.
func (s *Store) CreateAPIKey(ctx context.Context, key APIKey, tokenHash string) (APIKey, error) {
	db, err := s.directoryDB()
	if err != nil {
		return APIKey{}, err
	}
	tokenHash = strings.ToLower(strings.TrimSpace(tokenHash))
	if tokenHash == "" {
		return APIKey{}, errors.New("token hash is required")
	}
	if key.ID == "" {
		key.ID = NewID("key")
	}
	key.Tenant = strings.ToLower(strings.TrimSpace(key.Tenant))
	key.CreatedAt = time.Now().UTC()

	_, err = db.ExecContext(ctx, `
INSERT INTO promtact_api_keys (id, tenant, user_id, name, token_sha256, created_at)
VALUES ($1, $2, $3, $4, $5, $6)`,
		key.ID, key.Tenant, key.UserID, key.Name, tokenHash, key.CreatedAt)
	if err != nil {
		return APIKey{}, err
	}
	return key, nil
}

func (s *Store) ListAPIKeys(ctx context.Context, tenant string) ([]APIKey, error) {
	db, err := s.directoryDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT id, tenant, user_id, name, created_at, last_used_at, revoked_at
FROM promtact_api_keys WHERE tenant = $1 ORDER BY created_at ASC`,
		strings.ToLower(strings.TrimSpace(tenant)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make([]APIKey, 0, 8)
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Tenant, &k.UserID, &k.Name, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// RevokeAPIKey disables a key immediately; the next request using it fails.
func (s *Store) RevokeAPIKey(ctx context.Context, tenant string, id string) error {
	db, err := s.directoryDB()
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `
UPDATE promtact_api_keys SET revoked_at = now()
WHERE id = $1 AND tenant = $2 AND revoked_at IS NULL`,
		strings.TrimSpace(id), strings.ToLower(strings.TrimSpace(tenant)))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("api key not found or already revoked")
	}
	return nil
}

// IdentityByTokenHash resolves an API key hash to a principal. It requires an
// unrevoked key, an active user, and an active tenant, so revocation and tenant
// suspension take effect on the very next request.
//
// The returned error separates "this identity does not exist" (nil error, false)
// from "the directory could not be reached" (non-nil error), because only the
// latter may be answered from a cache.
func (s *Store) IdentityByTokenHash(ctx context.Context, tokenHash string) (Identity, bool, error) {
	db, err := s.directoryDB()
	if err != nil {
		return Identity{}, false, err
	}
	tokenHash = strings.ToLower(strings.TrimSpace(tokenHash))
	if tokenHash == "" {
		return Identity{}, false, nil
	}

	var keyID, name, tenant, roles string
	err = db.QueryRowContext(ctx, `
SELECT k.id, u.name, u.tenant, u.roles
FROM promtact_api_keys k
JOIN promtact_tenant_users u ON u.id = k.user_id
JOIN promtact_tenant_accounts t ON t.tenant = u.tenant
WHERE k.token_sha256 = $1
  AND k.revoked_at IS NULL
  AND u.status = 'active'
  AND t.status = 'active'`, tokenHash).Scan(&keyID, &name, &tenant, &roles)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, err
	}

	// Track usage at most once per 5 minutes so the auth path stays cheap.
	_, _ = db.ExecContext(ctx, `
UPDATE promtact_api_keys SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '5 minutes')`, keyID)

	return Identity{Name: name, Tenant: tenant, Roles: decodeRoles(roles)}, true, nil
}

// IdentityByCredentials resolves a user name plus API key hash, for the
// dashboard login form. The key must belong to that same user.
func (s *Store) IdentityByCredentials(ctx context.Context, username string, tokenHash string) (Identity, bool, error) {
	db, err := s.directoryDB()
	if err != nil {
		return Identity{}, false, err
	}
	username = strings.TrimSpace(username)
	tokenHash = strings.ToLower(strings.TrimSpace(tokenHash))
	if username == "" || tokenHash == "" {
		return Identity{}, false, nil
	}

	var name, tenant, roles string
	err = db.QueryRowContext(ctx, `
SELECT u.name, u.tenant, u.roles
FROM promtact_api_keys k
JOIN promtact_tenant_users u ON u.id = k.user_id
JOIN promtact_tenant_accounts t ON t.tenant = u.tenant
WHERE k.token_sha256 = $1
  AND lower(u.name) = lower($2)
  AND k.revoked_at IS NULL
  AND u.status = 'active'
  AND t.status = 'active'`, tokenHash, username).Scan(&name, &tenant, &roles)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, err
	}
	return Identity{Name: name, Tenant: tenant, Roles: decodeRoles(roles)}, true, nil
}

func encodeRoles(roles []string) string {
	cleaned := make([]string, 0, len(roles))
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		cleaned = append(cleaned, role)
	}
	sort.Strings(cleaned)
	return strings.Join(cleaned, ",")
}

func decodeRoles(value string) []string {
	parts := strings.Split(value, ",")
	roles := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			roles = append(roles, part)
		}
	}
	return roles
}
