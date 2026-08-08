package witness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"
)

// A signature produced by a real Web Crypto implementation, using exactly the
// code in deploy/cloudflare-worker/worker.js.
//
// This is the fixture that matters. The Worker signs with Web Crypto and this
// package verifies with Go's crypto/ecdsa, and the two disagree in two ways
// that are easy to get wrong and impossible to notice from either side alone:
// Web Crypto emits raw r||s while Go's VerifyASN1 expects ASN.1 DER, and the
// signed string has to match byte for byte across two languages. A hand-rolled
// Go-signs-Go-verifies test would pass happily while the Worker's receipts were
// rejected in production.
//
// Regenerate by importing the Worker's own functions - see the harness in the
// commit that added this - so the fixture cannot drift from the deployed code.
//
// Previously regenerated with:
//
//	node -e "const c=require('crypto').webcrypto; ..."
const (
	workerPublicKey = "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE30QhQwIbF8J2L//GzE7WsdN8X/HPoCDkuC0WlNs1Ogje9zA1HZWrbpHd4YUE+vvLoFeL10WNeRoTLE0dAOOnHg=="
	workerSignature = "gde+HPj5TZtic9nwH96waaUfixhb6rnMQ8fFzsqlnnxa6xbkoX8IeRsH6Uu1LHRcraDK3JUKu8h0S+UlsRyrjQ=="
	workerSignedMsg = "promtact-witness-v1|512|deadbeef99|2026-08-08T15:38:10.390Z"

	// Milliseconds, and they are the point of this fixture.
	//
	// The first version of this package parsed witnessed_at into a time.Time and
	// re-rendered it with time.RFC3339, which drops sub-second precision.
	// JavaScript's toISOString() always emits it. Every receipt the real Worker
	// produced was therefore rejected as a forgery - and every test here passed,
	// because the fake witness they used truncated to whole seconds.
	workerWitnessedAt = "2026-08-08T15:38:10.390Z"
)

func workerReceipt() Receipt {
	return Receipt{
		ChainIndex:  512,
		Head:        "DEADbeef99", // upper case on purpose: the signing string lower-cases it
		WitnessedAt: workerWitnessedAt,
		Signature:   workerSignature,
		KeyID:       "w1",
	}
}

// The signing strings must agree across the two languages, or nothing else
// matters. Checked separately so a mismatch names itself instead of surfacing
// as an opaque signature failure.
func TestSigningStringMatchesTheWorker(t *testing.T) {
	if got := workerReceipt().SigningString(); got != workerSignedMsg {
		t.Fatalf("signing string drifted from the Worker:\n go: %s\n js: %s", got, workerSignedMsg)
	}
}

func TestVerifiesARealWebCryptoSignature(t *testing.T) {
	key, err := ParsePublicKey(workerPublicKey)
	if err != nil {
		t.Fatalf("parsing the exported SPKI key: %v", err)
	}
	if err := workerReceipt().Verify(key); err != nil {
		t.Fatalf("a signature produced by Web Crypto was rejected: %v", err)
	}
}

// The point of a signature is that a changed statement stops verifying. Each
// field is altered on its own, so a field accidentally left out of the signing
// string is caught rather than averaged away.
func TestATamperedReceiptFailsVerification(t *testing.T) {
	key, err := ParsePublicKey(workerPublicKey)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	for name, mutate := range map[string]func(*Receipt){
		"a different index": func(r *Receipt) { r.ChainIndex = 342 },
		"a different head":  func(r *Receipt) { r.Head = "deadbeef" },
		"a different time":  func(r *Receipt) { r.WitnessedAt = "2026-08-08T15:38:11.390Z" },
		"a dropped millisecond": func(r *Receipt) {
			// The exact regression: same instant to the second, different text.
			r.WitnessedAt = "2026-08-08T15:38:10Z"
		},
	} {
		t.Run(name, func(t *testing.T) {
			receipt := workerReceipt()
			mutate(&receipt)
			if err := receipt.Verify(key); err == nil {
				t.Fatalf("%s still verified, so that field is not covered by the signature", name)
			}
		})
	}
}

// A receipt signed by some other key must not pass. Without this, "verified"
// would mean "well-formed".
func TestAReceiptFromAnotherKeyFails(t *testing.T) {
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	receipt := workerReceipt()
	digest := sha256.Sum256([]byte(receipt.SigningString()))
	r, s, err := ecdsa.Sign(rand.Reader, other, digest[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	s.FillBytes(raw[32:])
	receipt.Signature = base64.StdEncoding.EncodeToString(raw)

	key, err := ParsePublicKey(workerPublicKey)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if err := receipt.Verify(key); err == nil {
		t.Fatal("a receipt signed by an unrelated key verified against the witness key")
	}

	// And it must verify against its own key, or the test above proves only
	// that the signature was malformed.
	derPublic, err := x509.MarshalPKIXPublicKey(&other.PublicKey)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	ownKey, err := ParsePublicKey(base64.StdEncoding.EncodeToString(derPublic))
	if err != nil {
		t.Fatalf("parsing own key: %v", err)
	}
	if err := receipt.Verify(ownKey); err != nil {
		t.Fatalf("the signature does not verify against its own key either: %v", err)
	}
}

// An unsigned receipt is what an older witness returns. It must be reported as
// unsigned rather than counted as valid or as a failure: the first would be a
// false assurance, the second a false alarm during a rollout.
func TestUnsignedReceiptsAreCountedSeparately(t *testing.T) {
	key, err := ParsePublicKey(workerPublicKey)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	broken := workerReceipt()
	broken.ChainIndex = 999

	result := VerifyAll([]Receipt{
		workerReceipt(),
		{ChainIndex: 12, Head: "abc", WitnessedAt: "2026-08-08T12:00:00Z"},
		broken,
	}, key)

	if result.Valid != 1 || result.Unsigned != 1 || len(result.Failures) != 1 {
		t.Fatalf("got valid=%d unsigned=%d failures=%d, want 1/1/1 (%v)",
			result.Valid, result.Unsigned, len(result.Failures), result.Failures)
	}
	if result.HighestWitnessed != 512 {
		t.Errorf("highest witnessed index is %d, want 512", result.HighestWitnessed)
	}
}

func TestParsePublicKeyAcceptsPEM(t *testing.T) {
	pem := "-----BEGIN PUBLIC KEY-----\n" + workerPublicKey + "\n-----END PUBLIC KEY-----"
	if _, err := ParsePublicKey(pem); err != nil {
		t.Fatalf("a PEM-wrapped key was rejected: %v", err)
	}
	if _, err := ParsePublicKey("   "); err == nil {
		t.Fatal("an empty key was accepted")
	}
	if _, err := ParsePublicKey("not base64 at all !!"); err == nil {
		t.Fatal("a malformed key was accepted")
	}
	if !strings.Contains(mustErr(t, "AAAA"), "PKIX") {
		t.Error("the error for a non-PKIX key should say so")
	}
}

func mustErr(t *testing.T, value string) string {
	t.Helper()
	_, err := ParsePublicKey(value)
	if err == nil {
		t.Fatalf("%q was accepted as a key", value)
	}
	return err.Error()
}
