package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/store"
)

// The second factor sits between proving a credential and receiving a session.
//
// Two failure modes drive the design. Enabling MFA for a tenant must not lock
// out the people who have not enrolled yet — so an unenrolled user is refused
// with a distinct, actionable reason rather than a generic rejection, and an
// admin can enrol them. And the check must fail closed: if the enrolment state
// cannot be read, the login is refused, because the alternative is letting
// everyone in without a second factor exactly when the database is unhealthy.

var (
	errMFARequired    = errors.New("a multi-factor code is required")
	errMFAInvalid     = errors.New("the multi-factor code is not valid")
	errMFAEnrolNeeded = errors.New("this tenant requires multi-factor authentication and this account is not enrolled")
)

// mfaOutcome distinguishes the reasons a second factor can block a login, so
// the console can tell a user to open their authenticator app rather than
// telling them their password was wrong.
type mfaOutcome int

const (
	mfaSatisfied mfaOutcome = iota
	mfaCodeMissing
	mfaCodeInvalid
	mfaEnrolmentMissing
)

// checkSecondFactor decides whether a verified principal may receive a session.
// The submitted code may be either a TOTP code or a single-use recovery code.
func (a *App) checkSecondFactor(ctx context.Context, principal auth.Principal, code string) (mfaOutcome, error) {
	if !a.store.HasDirectory() {
		// Self-hosted single-tenant installs authenticate against policy.json
		// and have no directory to enrol into; MFA does not apply there.
		return mfaSatisfied, nil
	}

	user, found, err := a.store.UserByName(ctx, principal.Name)
	if err != nil {
		return mfaCodeInvalid, err
	}
	if !found {
		// A configured (policy.json) user authenticated; there is no directory
		// record to require a factor against.
		return mfaSatisfied, nil
	}

	status, err := a.store.MFAStatusFor(ctx, user.ID, user.Tenant)
	if err != nil {
		return mfaCodeInvalid, err
	}

	// A confirmed enrolment is always honoured, even if the tenant has not made
	// it mandatory: someone who deliberately turned on a second factor must not
	// have it silently ignored.
	if !status.Confirmed {
		if status.TenantRequired {
			return mfaEnrolmentMissing, nil
		}
		return mfaSatisfied, nil
	}

	code = strings.TrimSpace(code)
	if code == "" {
		return mfaCodeMissing, nil
	}

	secret, confirmed, ok, err := a.store.MFASecret(ctx, user.ID)
	if err != nil {
		return mfaCodeInvalid, err
	}
	if !ok || !confirmed {
		return mfaCodeInvalid, nil
	}

	if step, valid := auth.VerifyTOTP(secret, code, time.Now().UTC()); valid {
		// A TOTP code stays valid for its whole window, so claiming the step is
		// what stops an intercepted code being used a second time.
		fresh, err := a.store.ConsumeTOTPStep(ctx, user.ID, step)
		if err != nil {
			return mfaCodeInvalid, err
		}
		if !fresh {
			return mfaCodeInvalid, nil
		}
		return mfaSatisfied, nil
	}

	// Not a TOTP code — it may be a recovery code, which is single-use.
	used, err := a.store.ConsumeRecoveryCode(ctx, user.ID, auth.HashToken(code))
	if err != nil {
		return mfaCodeInvalid, err
	}
	if used {
		return mfaSatisfied, nil
	}
	return mfaCodeInvalid, nil
}

// handleMFAEnroll issues a new secret and a set of recovery codes. The secret
// and the codes are returned exactly once and never persisted in the clear on
// the client side; the enrolment stays inactive until a code confirms it, so a
// mistyped setup cannot lock anyone out.
func (a *App) handleMFAEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	principal := principalFromRequest(r)
	user, ok := a.mfaSubject(w, r, principal)
	if !ok {
		return
	}
	if user.Kind == store.KindService {
		writeError(w, http.StatusBadRequest, errors.New("service accounts do not use interactive login and cannot enrol a second factor"))
		return
	}

	secret, err := auth.NewTOTPSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	codes := make([]string, 0, 8)
	hashes := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		code, err := auth.NewRecoveryCode()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		codes = append(codes, code)
		hashes = append(hashes, auth.HashToken(code))
	}

	if err := a.store.EnrollMFA(r.Context(), user.ID, secret, hashes); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	a.recordAudit(r, principal, "auth.mfa.enroll", "user", user.ID, "pending", map[string]string{
		"tenant": user.Tenant,
	})

	writeJSON(w, http.StatusCreated, map[string]any{
		"user_id":        user.ID,
		"secret":         secret,
		"otpauth_url":    auth.TOTPURI("Promtact", user.Name, secret),
		"recovery_codes": codes,
		"note":           "Confirm with a generated code to activate. The secret and recovery codes are shown only once.",
	})
}

// handleMFAConfirm activates a pending enrolment once its owner produces a code
// from it, proving the secret was transferred correctly.
func (a *App) handleMFAConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	principal := principalFromRequest(r)
	user, ok := a.mfaSubject(w, r, principal)
	if !ok {
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	secret, confirmed, found, err := a.store.MFASecret(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, errors.New("no enrolment is pending for this account"))
		return
	}
	if confirmed {
		writeError(w, http.StatusConflict, errors.New("multi-factor authentication is already active"))
		return
	}

	step, valid := auth.VerifyTOTP(secret, strings.TrimSpace(req.Code), time.Now().UTC())
	if !valid {
		a.recordAudit(r, principal, "auth.mfa.confirm", "user", user.ID, "denied", nil)
		writeError(w, http.StatusUnauthorized, errMFAInvalid)
		return
	}
	if _, err := a.store.ConsumeTOTPStep(r.Context(), user.ID, step); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := a.store.ConfirmMFA(r.Context(), user.ID); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	a.recordAudit(r, principal, "auth.mfa.confirm", "user", user.ID, "accepted", map[string]string{
		"tenant": user.Tenant,
	})
	writeJSON(w, http.StatusOK, map[string]any{"user_id": user.ID, "confirmed": true})
}

// handleMFAStatus reports the caller's own enrolment state.
func (a *App) handleMFAStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	principal := principalFromRequest(r)
	user, ok := a.mfaSubject(w, r, principal)
	if !ok {
		return
	}
	status, err := a.store.MFAStatusFor(r.Context(), user.ID, user.Tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// mfaSubject resolves the directory record the caller is acting on. Enrolment
// is self-service only: a caller may configure their own second factor and
// nobody else's, so a stolen admin session cannot re-enrol another account and
// take it over.
func (a *App) mfaSubject(w http.ResponseWriter, r *http.Request, principal auth.Principal) (store.TenantUser, bool) {
	if !a.store.HasDirectory() {
		writeError(w, http.StatusNotImplemented, errors.New("multi-factor authentication requires the tenant directory"))
		return store.TenantUser{}, false
	}
	user, found, err := a.store.UserByName(r.Context(), principal.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return store.TenantUser{}, false
	}
	if !found {
		writeError(w, http.StatusNotFound, errors.New("this principal has no directory account"))
		return store.TenantUser{}, false
	}
	return user, true
}

// mfaLoginFailure maps a blocked second factor onto a response. The reasons are
// deliberately distinguishable: a user who is told "wrong credentials" when the
// real problem is a missing code will retype their key until they are locked
// out. The distinction leaks only that the first factor was correct, which the
// attempt itself already establishes for whoever holds that credential.
func mfaLoginFailure(outcome mfaOutcome) (int, string, error) {
	switch outcome {
	case mfaCodeMissing:
		return http.StatusUnauthorized, "mfa_code_required", errMFARequired
	case mfaEnrolmentMissing:
		return http.StatusForbidden, "mfa_enrolment_required", errMFAEnrolNeeded
	default:
		return http.StatusUnauthorized, "mfa_code_invalid", errMFAInvalid
	}
}
