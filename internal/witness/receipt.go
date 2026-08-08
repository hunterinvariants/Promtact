// Package witness verifies signed receipts issued by an external audit witness.
//
// Publishing a head to a witness already turns a rewritten history into a
// disagreement somebody can notice. But noticing requires asking the witness,
// and the answer is only as good as the witness being reachable, honest, and
// still in business at the moment the question is asked. An auditor a year from
// now, handed a database and no network access, could check nothing.
//
// A receipt closes that. The witness signs what it saw and when, the gateway
// stores that signature next to the chain, and the signature can be checked
// later by anyone holding the public key - offline, without the witness, and
// without trusting the operator who handed over the data.
//
// The important consequence is not that receipts prove the chain is good. It is
// that a *missing* receipt stops being an absence of evidence. Every record the
// witness accepted has one; a range with no receipt is a range that was never
// witnessed, and that is a finding rather than a shrug.
package witness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// receiptDomain separates these signatures from any other use of the same key.
// Without a domain prefix a signature over "12|abc" could be replayed as a
// signature over something else that happens to serialise identically, and
// version it so a future format change cannot be confused with this one.
const receiptDomain = "promtact-witness-v1"

// Receipt is a witness's signed statement that it saw a given chain head at a
// given index, at a given time.
type Receipt struct {
	ChainIndex int    `json:"chain_index"`
	Head       string `json:"head"`

	// WitnessedAt is deliberately a string, kept exactly as the witness sent
	// it, and never re-rendered before being signed over.
	//
	// It was a time.Time, and every receipt the real Worker produced failed to
	// verify: JavaScript's toISOString() emits milliseconds, Go's RFC3339
	// layout drops them, and the signing strings differed by ".390". Every Go
	// test passed, because the fake witness in those tests happened to truncate
	// to whole seconds. Parsing a timestamp and re-formatting it is a lossy
	// round trip, and doing that inside a signature check turns any precision
	// difference into a forgery report.
	WitnessedAt string `json:"witnessed_at"`

	// Signature is base64 raw r||s over SigningString, as Web Crypto produces
	// for ECDSA P-256. KeyID names the key so a rotation stays checkable.
	Signature string `json:"signature,omitempty"`
	KeyID     string `json:"key_id,omitempty"`
}

// Signed reports whether this receipt carries a signature at all. An unsigned
// receipt is what an older witness returns, and it is worth distinguishing from
// one whose signature fails to verify: the first is unupgraded, the second is
// evidence.
func (r Receipt) Signed() bool { return strings.TrimSpace(r.Signature) != "" }

// SigningString is the exact byte sequence the witness signs.
//
// It is built from the fields rather than from the received JSON on purpose:
// verifying a re-serialised JSON document would make the result depend on key
// order and whitespace, and a verifier that can be broken by a reformatting
// proxy is not one anybody should rely on.
func (r Receipt) SigningString() string {
	return strings.Join([]string{
		receiptDomain,
		strconv.Itoa(r.ChainIndex),
		strings.ToLower(strings.TrimSpace(r.Head)),
		strings.TrimSpace(r.WitnessedAt),
	}, "|")
}

// WitnessedTime parses the timestamp for display and ordering. It is separate
// from SigningString on purpose: verification uses the original text, and only
// presentation is allowed to depend on parsing succeeding.
func (r Receipt) WitnessedTime() (time.Time, error) {
	return time.Parse(time.RFC3339, strings.TrimSpace(r.WitnessedAt))
}

// ParsePublicKey reads the witness's public key as base64-encoded PKIX/SPKI,
// which is what Web Crypto's exportKey("spki", ...) produces.
func ParsePublicKey(encoded string) (*ecdsa.PublicKey, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, errors.New("no witness public key configured")
	}
	// Tolerate a PEM wrapper, because an operator copying a key out of a file
	// will paste one sooner or later.
	if strings.Contains(encoded, "-----BEGIN") {
		encoded = stripPEM(encoded)
	}
	der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(encoded), ""))
	if err != nil {
		return nil, fmt.Errorf("the witness public key is not valid base64: %w", err)
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("the witness public key is not a PKIX key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("the witness public key is %T, want an ECDSA key", parsed)
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("the witness public key is not on P-256")
	}
	return key, nil
}

func stripPEM(value string) string {
	var out strings.Builder
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}
		out.WriteString(line)
	}
	return out.String()
}

// Verify checks the receipt's signature against the witness public key.
func (r Receipt) Verify(key *ecdsa.PublicKey) error {
	if key == nil {
		return errors.New("no witness public key configured")
	}
	if !r.Signed() {
		return errors.New("the receipt carries no signature")
	}
	raw, err := decodeSignature(r.Signature)
	if err != nil {
		return err
	}
	// Web Crypto emits the P-256 signature as raw r||s, two fixed 32-byte
	// halves, rather than the ASN.1 DER that Go's VerifyASN1 expects. Splitting
	// it here is the whole of the conversion.
	if len(raw) != 64 {
		return fmt.Errorf("the signature is %d bytes, want 64 (raw r||s for P-256)", len(raw))
	}
	rInt := new(big.Int).SetBytes(raw[:32])
	sInt := new(big.Int).SetBytes(raw[32:])

	digest := sha256.Sum256([]byte(r.SigningString()))
	if !ecdsa.Verify(key, digest[:], rInt, sInt) {
		return errors.New("the witness signature does not match this chain head")
	}
	return nil
}

// decodeSignature accepts standard or URL-safe base64, with or without padding.
// A signature that fails to verify is a serious finding, so it must not be
// possible to produce that finding merely by encoding it differently.
func decodeSignature(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if raw, err := encoding.DecodeString(value); err == nil {
			return raw, nil
		}
	}
	return nil, errors.New("the signature is not valid base64")
}

// VerificationResult summarises a set of receipts against the local chain.
type VerificationResult struct {
	Checked  int
	Valid    int
	Unsigned int
	// Failures are receipts whose signature did not verify. A non-empty list
	// means the stored receipt is not one this witness key produced.
	Failures []string
	// HighestWitnessed is the largest index carrying a valid signature.
	HighestWitnessed int
}

// VerifyAll checks every receipt and reports what holds.
func VerifyAll(receipts []Receipt, key *ecdsa.PublicKey) VerificationResult {
	result := VerificationResult{HighestWitnessed: -1}
	for _, receipt := range receipts {
		result.Checked++
		if !receipt.Signed() {
			result.Unsigned++
			continue
		}
		if err := receipt.Verify(key); err != nil {
			result.Failures = append(result.Failures,
				fmt.Sprintf("record %d: %v", receipt.ChainIndex, err))
			continue
		}
		result.Valid++
		if receipt.ChainIndex > result.HighestWitnessed {
			result.HighestWitnessed = receipt.ChainIndex
		}
	}
	return result
}
