package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP as specified in RFC 6238, implemented against the standard library.
//
// The algorithm is thirty lines of HMAC and modular arithmetic; a dependency
// for it would add a supply-chain edge to the authentication path — the single
// place in this system where a compromised package is worth the most to an
// attacker. HMAC-SHA1 is fixed rather than configurable because it is what
// every authenticator app implements, and its use here is as a PRF, not as a
// collision-resistant hash.

const (
	totpDigits   = 6
	totpPeriod   = 30 * time.Second
	totpSkewStep = 1 // accept one step either side, for clock drift
)

// NewTOTPSecret returns a fresh base32 secret suitable for an authenticator app.
func NewTOTPSecret() (string, error) {
	buf := make([]byte, 20) // 160 bit, the size RFC 4226 recommends for SHA-1
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.TrimRight(base32.StdEncoding.EncodeToString(buf), "="), nil
}

// TOTPURI builds the otpauth:// URI an authenticator app scans. The secret is
// carried in the URI, so this value must be shown to its owner once and never
// logged or persisted.
func TOTPURI(issuer string, account string, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprintf("%d", totpDigits))
	query.Set("period", fmt.Sprintf("%d", int(totpPeriod.Seconds())))
	return "otpauth://totp/" + label + "?" + query.Encode()
}

// TOTPTimeStep is the counter a code belongs to. The caller records it so the
// same code cannot be replayed while its window is still open.
func TOTPTimeStep(at time.Time) int64 {
	return at.UTC().Unix() / int64(totpPeriod.Seconds())
}

func totpCode(secret string, step int64) (string, bool) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(key) == 0 {
		return "", false
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))

	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, truncated%1_000_000), true
}

// VerifyTOTP checks a code against the secret, tolerating one step of clock
// drift in each direction. It returns the time step the code belonged to so the
// caller can reject a replay of that same code.
//
// Comparison is constant-time: a code is a six-digit shared secret, and leaking
// how many leading digits matched would reduce it to a handful of guesses.
func VerifyTOTP(secret string, code string, at time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	current := TOTPTimeStep(at)
	for delta := int64(-totpSkewStep); delta <= totpSkewStep; delta++ {
		step := current + delta
		expected, ok := totpCode(secret, step)
		if !ok {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// NewRecoveryCode returns a single-use code for the case where an authenticator
// device is lost. It is shown once and stored only as a hash.
func NewRecoveryCode() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	encoded := strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(buf), "="))
	return encoded[:4] + "-" + encoded[4:8] + "-" + encoded[8:12] + "-" + encoded[12:], nil
}
