package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/crypto"
)

// Multi-factor enrolment state for human accounts.
//
// Two rules shape this code. Enrolment is not enforcement: a secret only counts
// once its owner has proven they can generate a code from it, otherwise a
// mistyped setup locks a person out of their own tenant. And every consumption
// step is a conditional UPDATE rather than a read-then-write, so two concurrent
// logins cannot both spend the same code.

type MFAStatus struct {
	Enrolled       bool       `json:"enrolled"`
	Confirmed      bool       `json:"confirmed"`
	ConfirmedAt    *time.Time `json:"confirmed_at,omitempty"`
	RecoveryLeft   int        `json:"recovery_codes_remaining"`
	TenantRequired bool       `json:"tenant_requires_mfa"`
}

// SetSealer attaches envelope encryption for secrets at rest. It is called
// during startup, before the store serves traffic. Without one, values are
// stored as before, so enabling encryption is opt-in and reversible for records
// written while it was off.
func (s *Store) SetSealer(sealer *crypto.Sealer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sealer = sealer
}

func (s *Store) currentSealer() *crypto.Sealer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sealer
}

// SetTenantMFARequired turns the second factor on or off for a whole tenant.
func (s *Store) SetTenantMFARequired(ctx context.Context, tenant string, required bool) error {
	db, err := s.directoryDB()
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx,
		`UPDATE promtact_tenant_accounts SET mfa_required = $2 WHERE tenant = $1`,
		strings.ToLower(strings.TrimSpace(tenant)), required)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("tenant not found")
	}
	return nil
}

// UserByName resolves a login name to its directory record.
func (s *Store) UserByName(ctx context.Context, username string) (TenantUser, bool, error) {
	db, err := s.directoryDB()
	if err != nil {
		return TenantUser{}, false, err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return TenantUser{}, false, nil
	}
	var user TenantUser
	var roles string
	err = db.QueryRowContext(ctx, `
SELECT id, tenant, name, roles, kind, status, created_at
FROM promtact_tenant_users WHERE lower(name) = lower($1)`, username).
		Scan(&user.ID, &user.Tenant, &user.Name, &roles, &user.Kind, &user.Status, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TenantUser{}, false, nil
	}
	if err != nil {
		return TenantUser{}, false, err
	}
	user.Roles = decodeRoles(roles)
	return user, true, nil
}

// EnrollMFA stores an unconfirmed secret, replacing any previous enrolment that
// was never confirmed. A confirmed enrolment is not overwritten: that would let
// anyone holding a valid session silently swap out the second factor.
func (s *Store) EnrollMFA(ctx context.Context, userID string, secret string, recoveryHashes []string) error {
	db, err := s.directoryDB()
	if err != nil {
		return err
	}
	userID = strings.TrimSpace(userID)
	secret = strings.TrimSpace(secret)
	if userID == "" || secret == "" {
		return errors.New("user id and secret are required")
	}

	// A TOTP seed has to be readable to verify a code, so unlike an API key it
	// cannot be reduced to a hash. It is sealed instead, and a failure here
	// aborts the enrolment rather than falling back to storing it in the clear.
	secret, err = s.currentSealer().Seal(ctx, secret)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
INSERT INTO promtact_user_mfa (user_id, secret, confirmed_at, created_at)
VALUES ($1, $2, NULL, now())
ON CONFLICT (user_id) DO UPDATE SET secret = EXCLUDED.secret, created_at = now()
WHERE promtact_user_mfa.confirmed_at IS NULL`, userID, secret)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("multi-factor authentication is already confirmed for this user")
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM promtact_mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, hash := range recoveryHashes {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO promtact_mfa_recovery_codes (id, user_id, code_sha256) VALUES ($1, $2, $3)`,
			NewID("rec"), userID, strings.ToLower(strings.TrimSpace(hash))); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MFASecret returns the enrolled secret and whether it has been confirmed.
func (s *Store) MFASecret(ctx context.Context, userID string) (secret string, confirmed bool, found bool, err error) {
	db, dbErr := s.directoryDB()
	if dbErr != nil {
		return "", false, false, dbErr
	}
	var confirmedAt sql.NullTime
	err = db.QueryRowContext(ctx,
		`SELECT secret, confirmed_at FROM promtact_user_mfa WHERE user_id = $1`,
		strings.TrimSpace(userID)).Scan(&secret, &confirmedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	secret, err = s.currentSealer().Open(ctx, secret)
	if err != nil {
		return "", false, false, err
	}
	return secret, confirmedAt.Valid, true, nil
}

// ConfirmMFA activates an enrolment once its owner has produced a valid code.
func (s *Store) ConfirmMFA(ctx context.Context, userID string) error {
	db, err := s.directoryDB()
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx,
		`UPDATE promtact_user_mfa SET confirmed_at = now() WHERE user_id = $1 AND confirmed_at IS NULL`,
		strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("no pending enrolment for this user")
	}
	return nil
}

// ConsumeTOTPStep records that a time step has been spent and reports whether
// this caller was the one to spend it. A TOTP code stays valid for its whole
// window, so without this an intercepted code could be used a second time.
//
// The insert itself is the lock: the primary key makes a duplicate step fail,
// so the check and the claim cannot be separated by a race.
func (s *Store) ConsumeTOTPStep(ctx context.Context, userID string, step int64) (bool, error) {
	db, err := s.directoryDB()
	if err != nil {
		return false, err
	}
	result, err := db.ExecContext(ctx, `
INSERT INTO promtact_mfa_used_codes (user_id, time_step) VALUES ($1, $2)
ON CONFLICT (user_id, time_step) DO NOTHING`, strings.TrimSpace(userID), step)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 1 {
		// Spent steps older than an hour can no longer be replayed, so the
		// table is pruned opportunistically instead of growing forever.
		_, _ = db.ExecContext(ctx,
			`DELETE FROM promtact_mfa_used_codes WHERE used_at < now() - interval '1 hour'`)
	}
	return affected == 1, nil
}

// ConsumeRecoveryCode spends a single-use recovery code. The conditional UPDATE
// means two simultaneous attempts cannot both succeed with the same code.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID string, codeHash string) (bool, error) {
	db, err := s.directoryDB()
	if err != nil {
		return false, err
	}
	result, err := db.ExecContext(ctx, `
UPDATE promtact_mfa_recovery_codes SET used_at = now()
WHERE user_id = $1 AND code_sha256 = $2 AND used_at IS NULL`,
		strings.TrimSpace(userID), strings.ToLower(strings.TrimSpace(codeHash)))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// MFAStatusFor reports enrolment state for a user, including whether their
// tenant requires a second factor at all.
func (s *Store) MFAStatusFor(ctx context.Context, userID string, tenant string) (MFAStatus, error) {
	db, err := s.directoryDB()
	if err != nil {
		return MFAStatus{}, err
	}
	var status MFAStatus
	var confirmedAt sql.NullTime
	err = db.QueryRowContext(ctx,
		`SELECT confirmed_at FROM promtact_user_mfa WHERE user_id = $1`, strings.TrimSpace(userID)).Scan(&confirmedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return MFAStatus{}, err
	default:
		status.Enrolled = true
		status.Confirmed = confirmedAt.Valid
		if confirmedAt.Valid {
			at := confirmedAt.Time.UTC()
			status.ConfirmedAt = &at
		}
	}

	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM promtact_mfa_recovery_codes WHERE user_id = $1 AND used_at IS NULL`,
		strings.TrimSpace(userID)).Scan(&status.RecoveryLeft); err != nil {
		return MFAStatus{}, err
	}
	if err := db.QueryRowContext(ctx,
		`SELECT mfa_required FROM promtact_tenant_accounts WHERE tenant = $1`,
		strings.ToLower(strings.TrimSpace(tenant))).Scan(&status.TenantRequired); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return MFAStatus{}, err
	}
	return status, nil
}

// DeactivateUser suspends an account and revokes every key it holds, in one
// transaction. Deprovisioning that leaves live credentials behind is the most
// common way an offboarded identity keeps working, so the two must not be
// separable.
func (s *Store) DeactivateUser(ctx context.Context, userID string) error {
	db, err := s.directoryDB()
	if err != nil {
		return err
	}
	userID = strings.TrimSpace(userID)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx,
		`UPDATE promtact_tenant_users SET status = $2 WHERE id = $1`, userID, StatusSuspended)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("user not found")
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE promtact_api_keys SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
		userID); err != nil {
		return err
	}
	return tx.Commit()
}

// ReactivateUser restores a suspended account. Revoked keys stay revoked: they
// may have been the reason for the suspension.
func (s *Store) ReactivateUser(ctx context.Context, userID string) error {
	db, err := s.directoryDB()
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx,
		`UPDATE promtact_tenant_users SET status = $2 WHERE id = $1`, strings.TrimSpace(userID), StatusActive)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("user not found")
	}
	return nil
}

// UserByID resolves a directory record within a tenant. The tenant is part of
// the lookup rather than checked afterwards, so a caller cannot reach another
// customer's user by guessing an id.
func (s *Store) UserByID(ctx context.Context, tenant string, id string) (TenantUser, bool, error) {
	db, err := s.directoryDB()
	if err != nil {
		return TenantUser{}, false, err
	}
	var user TenantUser
	var roles string
	err = db.QueryRowContext(ctx, `
SELECT id, tenant, name, roles, kind, status, created_at
FROM promtact_tenant_users WHERE id = $1 AND tenant = $2`,
		strings.TrimSpace(id), strings.ToLower(strings.TrimSpace(tenant))).
		Scan(&user.ID, &user.Tenant, &user.Name, &roles, &user.Kind, &user.Status, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TenantUser{}, false, nil
	}
	if err != nil {
		return TenantUser{}, false, err
	}
	user.Roles = decodeRoles(roles)
	return user, true, nil
}

// SetUserRoles replaces a user's roles within a tenant.
func (s *Store) SetUserRoles(ctx context.Context, tenant string, id string, roles []string) error {
	db, err := s.directoryDB()
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx,
		`UPDATE promtact_tenant_users SET roles = $3 WHERE id = $1 AND tenant = $2`,
		strings.TrimSpace(id), strings.ToLower(strings.TrimSpace(tenant)), encodeRoles(roles))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("user not found")
	}
	return nil
}
