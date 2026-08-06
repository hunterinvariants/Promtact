package server

import (
	"testing"

	"github.com/hunterinvariants/promtact/internal/auth"
)

// Enrolling a second factor acts only on the caller's own account, so every
// authenticated role must be able to do it. If it demanded admin, a viewer
// could not secure their own login and the tenant would simply never turn MFA
// on — the catch-all rule made exactly that mistake.
func TestSelfServiceMFAIsReachableByEveryRole(t *testing.T) {
	for _, path := range []string{"/api/auth/mfa", "/api/auth/mfa/enroll", "/api/auth/mfa/confirm"} {
		for _, method := range []string{"GET", "POST"} {
			required := auth.RequiredRoles(method, path)
			for _, role := range []string{auth.RoleViewer, auth.RoleIngestor, auth.RoleAnalyst, auth.RoleOperator} {
				principal := auth.Principal{Name: "someone", Roles: []string{role}}
				if !principal.HasAny(required...) {
					t.Errorf("%s %s is unreachable for %s", method, path, role)
				}
			}
		}
	}
}

// The reasons a second factor blocks a login must stay distinguishable. A user
// told "wrong credentials" when a code was merely missing will retype their key
// until the backoff locks them out.
func TestMFAFailuresAreDistinguishable(t *testing.T) {
	missingStatus, missingReason, _ := mfaLoginFailure(mfaCodeMissing)
	invalidStatus, invalidReason, _ := mfaLoginFailure(mfaCodeInvalid)
	enrolStatus, enrolReason, _ := mfaLoginFailure(mfaEnrolmentMissing)

	if missingReason == invalidReason || invalidReason == enrolReason || missingReason == enrolReason {
		t.Fatalf("reasons collide: %q %q %q", missingReason, invalidReason, enrolReason)
	}
	if missingStatus != 401 || invalidStatus != 401 {
		t.Errorf("a bad or missing code should be 401, got %d and %d", missingStatus, invalidStatus)
	}
	// Being unenrolled is not an authentication failure the user can retry
	// their way out of; it needs an operator, so it is 403 rather than 401.
	if enrolStatus != 403 {
		t.Errorf("a missing enrolment should be 403, got %d", enrolStatus)
	}
}

// Without a tenant directory there is nothing to enrol into, and single-tenant
// self-hosted installs must keep logging in exactly as before.
func TestSecondFactorIsSkippedWithoutADirectory(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if app.store.HasDirectory() {
		t.Skip("this build has a directory backend")
	}

	outcome, err := app.checkSecondFactor(t.Context(), auth.Principal{Name: "alice", Tenant: "default"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if outcome != mfaSatisfied {
		t.Fatalf("a directory-less install demanded a second factor: %v", outcome)
	}
}
