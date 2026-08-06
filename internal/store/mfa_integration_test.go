package store

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func mfaTestFixture(t *testing.T) (*Store, context.Context, string, string) {
	t.Helper()
	dsn := os.Getenv("PROMTACT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PROMTACT_TEST_POSTGRES_DSN to run Postgres integration tests")
	}
	s, err := NewWithPostgres(dsn)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	if s.SchemaVersion() < 7 {
		t.Fatalf("expected schema version >= 7 (service accounts and MFA), got %d", s.SchemaVersion())
	}

	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	tenant := "mfa-" + suffix
	if _, err := s.CreateTenantAccount(ctx, TenantAccount{Tenant: tenant}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user, err := s.CreateTenantUser(ctx, TenantUser{Tenant: tenant, Name: "alice-" + suffix, Roles: []string{"operator"}})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.Kind != KindHuman {
		t.Fatalf("a user created without a kind should default to human, got %q", user.Kind)
	}
	return s, ctx, tenant, user.ID
}

// A TOTP code stays valid for its entire time step. Without a claim on that
// step, a code read over someone's shoulder or captured from a phishing page
// remains usable for up to thirty more seconds — long enough to matter.
func TestTOTPStepCannotBeSpentTwice(t *testing.T) {
	s, ctx, _, userID := mfaTestFixture(t)

	step := time.Now().UTC().Unix() / 30
	first, err := s.ConsumeTOTPStep(ctx, userID, step)
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatal("the first use of a time step was refused")
	}
	second, err := s.ConsumeTOTPStep(ctx, userID, step)
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Fatal("the same time step was spent twice: a captured code is replayable")
	}

	// A different step is unaffected.
	other, err := s.ConsumeTOTPStep(ctx, userID, step+1)
	if err != nil {
		t.Fatal(err)
	}
	if !other {
		t.Fatal("an unrelated time step was refused")
	}
}

// Two logins racing with the same intercepted code must not both succeed.
// Exactly one claim may win, and the check-then-write must not be separable.
func TestConcurrentTOTPStepClaimsElectOneWinner(t *testing.T) {
	s, ctx, _, userID := mfaTestFixture(t)

	const racers = 16
	step := time.Now().UTC().Unix()/30 + 1000

	var wg sync.WaitGroup
	results := make([]bool, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ok, err := s.ConsumeTOTPStep(ctx, userID, step)
			if err != nil {
				t.Errorf("claim %d: %v", i, err)
			}
			results[i] = ok
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for _, ok := range results {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent claims won; exactly one may", winners, racers)
	}
}

// A recovery code is the fallback when a device is lost, so it must work
// exactly once. A reusable one is a permanent static password.
func TestRecoveryCodeIsSingleUse(t *testing.T) {
	s, ctx, _, userID := mfaTestFixture(t)

	const hash = "0000000000000000000000000000000000000000000000000000000000000001"
	if err := s.EnrollMFA(ctx, userID, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", []string{hash}); err != nil {
		t.Fatal(err)
	}

	used, err := s.ConsumeRecoveryCode(ctx, userID, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Fatal("a fresh recovery code was refused")
	}
	again, err := s.ConsumeRecoveryCode(ctx, userID, hash)
	if err != nil {
		t.Fatal(err)
	}
	if again {
		t.Fatal("a recovery code was accepted twice")
	}
	if _, err := s.ConsumeRecoveryCode(ctx, userID, "deadbeef"); err != nil {
		t.Fatal(err)
	}
}

// Enrolment must not be enforcement: a secret counts only once its owner has
// proven they can generate a code from it. And a confirmed enrolment must not
// be silently replaced, or anyone holding a session could swap out the factor.
func TestConfirmedEnrolmentCannotBeSilentlyReplaced(t *testing.T) {
	s, ctx, tenant, userID := mfaTestFixture(t)

	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if err := s.EnrollMFA(ctx, userID, secret, nil); err != nil {
		t.Fatal(err)
	}

	status, err := s.MFAStatusFor(ctx, userID, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enrolled || status.Confirmed {
		t.Fatalf("a fresh enrolment must be pending, not active: %+v", status)
	}

	// Re-enrolling while pending is allowed: setup may simply have gone wrong.
	if err := s.EnrollMFA(ctx, userID, "MZXW6YTBOI======MZXW6YTBOI======", nil); err != nil {
		t.Fatalf("replacing a pending enrolment was refused: %v", err)
	}

	if err := s.ConfirmMFA(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if err := s.EnrollMFA(ctx, userID, secret, nil); err == nil {
		t.Fatal("a confirmed second factor was silently replaced")
	}
	if err := s.ConfirmMFA(ctx, userID); err == nil {
		t.Fatal("confirming twice was allowed")
	}

	status, err = s.MFAStatusFor(ctx, userID, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Confirmed || status.ConfirmedAt == nil {
		t.Fatalf("the enrolment should be active: %+v", status)
	}
}

// Deprovisioning that leaves live credentials behind is the usual way an
// offboarded identity keeps working. Suspension and key revocation must be one
// atomic step, not two an operator can forget half of.
func TestDeactivationRevokesEveryKey(t *testing.T) {
	s, ctx, tenant, userID := mfaTestFixture(t)

	const hash = "00000000000000000000000000000000000000000000000000000000000000ff"
	if _, err := s.CreateAPIKey(ctx, APIKey{Tenant: tenant, UserID: userID, Name: "cli"}, hash); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.IdentityByTokenHash(ctx, hash); err != nil || !ok {
		t.Fatalf("the key should authenticate before deprovisioning (ok=%v err=%v)", ok, err)
	}

	if err := s.DeactivateUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.IdentityByTokenHash(ctx, hash); err != nil || ok {
		t.Fatalf("a deprovisioned user's key still authenticates (ok=%v err=%v)", ok, err)
	}

	// Reactivation must not silently resurrect the revoked keys: they may have
	// been the reason for the suspension in the first place.
	if err := s.ReactivateUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.IdentityByTokenHash(ctx, hash); err != nil || ok {
		t.Fatalf("reactivation resurrected a revoked key (ok=%v err=%v)", ok, err)
	}
}

// A service account has nobody behind it to present a second factor, so it must
// never be able to open an interactive session — while its key keeps working,
// since agents are the reason these accounts exist.
func TestServiceAccountCannotUseTheLoginForm(t *testing.T) {
	s, ctx, tenant, _ := mfaTestFixture(t)

	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	agent, err := s.CreateTenantUser(ctx, TenantUser{
		Tenant: tenant, Name: "agent-" + suffix, Roles: []string{"ingestor"}, Kind: KindService,
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Kind != KindService {
		t.Fatalf("the declared kind was not stored: %q", agent.Kind)
	}

	const hash = "00000000000000000000000000000000000000000000000000000000000000ab"
	if _, err := s.CreateAPIKey(ctx, APIKey{Tenant: tenant, UserID: agent.ID, Name: "agent-key"}, hash); err != nil {
		t.Fatal(err)
	}

	identity, ok, err := s.IdentityByTokenHash(ctx, hash)
	if err != nil || !ok {
		t.Fatalf("the service account's key must still authenticate (ok=%v err=%v)", ok, err)
	}
	if identity.Kind != KindService {
		t.Fatalf("the kind was lost on lookup: %q", identity.Kind)
	}

	if _, ok, err := s.IdentityByCredentials(ctx, agent.Name, hash); err != nil || ok {
		t.Fatalf("a service account resolved through the login path (ok=%v err=%v)", ok, err)
	}
}
