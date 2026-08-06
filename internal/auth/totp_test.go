package auth

import (
	"strings"
	"testing"
	"time"
)

// The RFC 6238 test vectors. Matching them is what makes the implementation
// interoperable with authenticator apps rather than merely self-consistent.
func TestTOTPMatchesRFC6238Vectors(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // base32 of "12345678901234567890"

	for _, tc := range []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	} {
		step := TOTPTimeStep(time.Unix(tc.unix, 0))
		got, ok := totpCode(secret, step)
		if !ok {
			t.Fatalf("T=%d: secret was rejected", tc.unix)
		}
		if got != tc.want {
			t.Errorf("T=%d: got %s, want %s", tc.unix, got, tc.want)
		}
	}
}

func TestVerifyTOTPAcceptsClockDrift(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	// One step of drift in either direction must still authenticate; a phone
	// whose clock is thirty seconds off is common, and locking those users out
	// would push operators to disable MFA entirely.
	for _, offset := range []time.Duration{-totpPeriod, 0, totpPeriod} {
		code, ok := totpCode(secret, TOTPTimeStep(now.Add(offset)))
		if !ok {
			t.Fatal("code generation failed")
		}
		if _, ok := VerifyTOTP(secret, code, now); !ok {
			t.Errorf("a code %s off was rejected", offset)
		}
	}

	// Beyond the tolerated window it must be refused, otherwise an old code
	// stays usable long after it was captured.
	stale, _ := totpCode(secret, TOTPTimeStep(now.Add(-10*totpPeriod)))
	if _, ok := VerifyTOTP(secret, stale, now); ok {
		t.Error("a code from ten steps ago was accepted")
	}
}

// The returned time step is what lets a caller refuse a replay, so it must
// identify the window the code actually came from.
func TestVerifyTOTPReportsItsTimeStep(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	want := TOTPTimeStep(now.Add(-totpPeriod))
	code, _ := totpCode(secret, want)

	got, ok := VerifyTOTP(secret, code, now)
	if !ok {
		t.Fatal("valid code rejected")
	}
	if got != want {
		t.Fatalf("reported step %d, want %d", got, want)
	}
}

func TestVerifyTOTPRejectsMalformedInput(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "     "} {
		if _, ok := VerifyTOTP(secret, bad, now); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
	// A secret that is not valid base32 must fail closed rather than panic or
	// verify against an empty key.
	code, _ := totpCode(secret, TOTPTimeStep(now))
	if _, ok := VerifyTOTP("not-base32-!!", code, now); ok {
		t.Error("an unparseable secret verified a code")
	}
}

func TestTOTPSecretsAreDistinct(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 50; i++ {
		secret, err := NewTOTPSecret()
		if err != nil {
			t.Fatal(err)
		}
		if len(secret) < 32 {
			t.Fatalf("secret is too short: %q", secret)
		}
		if _, dup := seen[secret]; dup {
			t.Fatal("a secret was generated twice")
		}
		seen[secret] = struct{}{}
	}
}

// The enrolment URI must carry everything an app needs, and must round-trip the
// secret unchanged — a mangled secret produces codes that never verify.
func TestTOTPURIIsScannable(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	uri := TOTPURI("Promtact", "alice@example.com", secret)
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("wrong scheme: %s", uri)
	}
	for _, needle := range []string{"secret=" + secret, "issuer=Promtact", "digits=6", "period=30", "algorithm=SHA1"} {
		if !strings.Contains(uri, needle) {
			t.Errorf("URI is missing %q: %s", needle, uri)
		}
	}
}

func TestRecoveryCodesAreDistinctAndFormatted(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 50; i++ {
		code, err := NewRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(code, "-") != 3 {
			t.Fatalf("unexpected recovery code shape: %q", code)
		}
		if _, dup := seen[code]; dup {
			t.Fatal("a recovery code was generated twice")
		}
		seen[code] = struct{}{}
	}
}
