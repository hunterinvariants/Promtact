package crypto

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testSealer(t *testing.T, primary string, keys map[string]string) *Sealer {
	t.Helper()
	provider, err := NewLocalKeyProvider(primary, keys)
	if err != nil {
		t.Fatal(err)
	}
	return NewSealer(provider)
}

func TestSealRoundTrip(t *testing.T) {
	sealer := testSealer(t, "k1", map[string]string{"k1": "a-sufficiently-long-key"})
	ctx := context.Background()

	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	sealed, err := sealer.Seal(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, secret) {
		t.Fatal("the plaintext survives in the sealed value")
	}
	if !IsSealed(sealed) {
		t.Fatalf("the sealed value is not recognisable: %q", sealed)
	}

	opened, err := sealer.Open(ctx, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if opened != secret {
		t.Fatalf("round trip changed the value: %q", opened)
	}
}

// Every record must get its own data key, so that recovering one does not
// unlock the rest of the table.
func TestEachRecordGetsItsOwnDataKey(t *testing.T) {
	sealer := testSealer(t, "k1", map[string]string{"k1": "a-sufficiently-long-key"})
	ctx := context.Background()

	first, err := sealer.Seal(ctx, "same value")
	if err != nil {
		t.Fatal(err)
	}
	second, err := sealer.Seal(ctx, "same value")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("sealing the same value twice produced identical ciphertext")
	}

	// The wrapped data keys must differ too, not just the nonce.
	firstWrapped := strings.Split(strings.TrimPrefix(first, envelopePrefix), ".")[1]
	secondWrapped := strings.Split(strings.TrimPrefix(second, envelopePrefix), ".")[1]
	if firstWrapped == secondWrapped {
		t.Fatal("the same data key was reused across records")
	}
}

// Rotation must not require rewriting every row at once: a new primary key
// signs new records while the old key keeps opening the existing ones.
func TestRotationKeepsOlderRecordsReadable(t *testing.T) {
	ctx := context.Background()
	old := testSealer(t, "2025a", map[string]string{"2025a": "the-older-key-value"})

	legacy, err := old.Seal(ctx, "enrolled last year")
	if err != nil {
		t.Fatal(err)
	}

	rotated := testSealer(t, "2026a", map[string]string{
		"2026a": "the-current-key-value",
		"2025a": "the-older-key-value",
	})

	opened, err := rotated.Open(ctx, legacy)
	if err != nil {
		t.Fatalf("a record from before the rotation became unreadable: %v", err)
	}
	if opened != "enrolled last year" {
		t.Fatalf("wrong plaintext after rotation: %q", opened)
	}

	// New records must use the new primary, or the rotation never completes.
	fresh, err := rotated.Seal(ctx, "enrolled today")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fresh, envelopePrefix+"2026a.") {
		t.Fatalf("a new record was not wrapped with the primary key: %q", fresh)
	}
}

// Dropping a key that records still reference must fail loudly. Treating it as
// a miss would surface as users mysteriously losing their second factor, with
// no indication that a key was the cause.
func TestARetiredKeyFailsLoudly(t *testing.T) {
	ctx := context.Background()
	old := testSealer(t, "2025a", map[string]string{"2025a": "the-older-key-value"})
	legacy, err := old.Seal(ctx, "enrolled last year")
	if err != nil {
		t.Fatal(err)
	}

	current := testSealer(t, "2026a", map[string]string{"2026a": "the-current-key-value"})
	_, err = current.Open(ctx, legacy)
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("expected an unknown-key error, got %v", err)
	}
	if !strings.Contains(err.Error(), "2025a") {
		t.Errorf("the error should name the missing key so a botched rotation is diagnosable: %v", err)
	}
}

// Turning encryption off while sealed records exist must not silently hand the
// ciphertext back as if it were the secret.
func TestOpeningASealedValueWithoutAKeyIsRefused(t *testing.T) {
	ctx := context.Background()
	sealer := testSealer(t, "k1", map[string]string{"k1": "a-sufficiently-long-key"})
	sealed, err := sealer.Seal(ctx, "secret")
	if err != nil {
		t.Fatal(err)
	}

	var disabled *Sealer
	if _, err := disabled.Open(ctx, sealed); !errors.Is(err, ErrNoKey) {
		t.Fatalf("expected a refusal, got %v", err)
	}
}

// An installation that never configured encryption must behave exactly as
// before, including through a nil Sealer.
func TestNilSealerPassesValuesThrough(t *testing.T) {
	ctx := context.Background()
	var sealer *Sealer
	if sealer.Enabled() {
		t.Fatal("a nil sealer reports itself as enabled")
	}
	sealed, err := sealer.Seal(ctx, "plain")
	if err != nil || sealed != "plain" {
		t.Fatalf("value was altered: %q %v", sealed, err)
	}
	opened, err := sealer.Open(ctx, "plain")
	if err != nil || opened != "plain" {
		t.Fatalf("value was altered: %q %v", opened, err)
	}
}

// Records written before encryption was enabled must keep working, so they can
// be migrated as they are rewritten rather than in one all-or-nothing pass.
func TestUnsealedLegacyValuesStillOpen(t *testing.T) {
	sealer := testSealer(t, "k1", map[string]string{"k1": "a-sufficiently-long-key"})
	opened, err := sealer.Open(context.Background(), "GEZDGNBVGY3TQOJQ")
	if err != nil {
		t.Fatal(err)
	}
	if opened != "GEZDGNBVGY3TQOJQ" {
		t.Fatalf("a legacy plaintext value was mangled: %q", opened)
	}
	if IsSealed("GEZDGNBVGY3TQOJQ") {
		t.Error("a plaintext value was reported as sealed")
	}
}

// Tampering must be detected rather than producing garbage plaintext: GCM
// authenticates, and the test exists to prove the tag is actually checked.
func TestTamperedCiphertextIsRejected(t *testing.T) {
	ctx := context.Background()
	sealer := testSealer(t, "k1", map[string]string{"k1": "a-sufficiently-long-key"})
	sealed, err := sealer.Seal(ctx, "secret value")
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(strings.TrimPrefix(sealed, envelopePrefix), ".")
	// The ciphertext is decoded before being altered. Flipping a bit in the
	// base64 text can be a no-op, because the surplus bits of the final
	// character are discarded on decoding — a tamper test that does that
	// passes without proving anything.
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[len(ciphertext)/2] ^= 0x01
	tampered := envelopePrefix + parts[0] + "." + parts[1] + "." +
		base64.RawURLEncoding.EncodeToString(ciphertext)

	if _, err := sealer.Open(ctx, tampered); err == nil {
		t.Fatal("a tampered ciphertext was accepted")
	}

	// The authentication tag must also cover the wrapped data key.
	wrapped, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	wrapped[len(wrapped)/2] ^= 0x01
	swapped := envelopePrefix + parts[0] + "." +
		base64.RawURLEncoding.EncodeToString(wrapped) + "." + parts[2]
	if _, err := sealer.Open(ctx, swapped); err == nil {
		t.Fatal("a tampered data key was accepted")
	}
}

// A short key produces the appearance of encryption at rest without the
// substance, so it must be refused at configuration time rather than at the
// first write.
func TestWeakConfigurationIsRefused(t *testing.T) {
	for name, keys := range map[string]map[string]string{
		"empty":     {},
		"short key": {"k1": "tooshort"},
		"no id":     {"": "a-sufficiently-long-key"},
		"no value":  {"k1": ""},
		"dot in id": {"k.1": "a-sufficiently-long-key"},
	} {
		if _, err := NewLocalKeyProvider("", keys); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// With several keys the primary must be named; guessing one would make
	// which key signs new records depend on map iteration order.
	if _, err := NewLocalKeyProvider("", map[string]string{
		"a": "a-sufficiently-long-key", "b": "another-long-enough-key",
	}); err == nil {
		t.Error("an ambiguous primary was accepted")
	}
	if _, err := NewLocalKeyProvider("c", map[string]string{
		"a": "a-sufficiently-long-key",
	}); err == nil {
		t.Error("a primary that is not among the keys was accepted")
	}
}

func TestKeyIDsAreListedWithoutMaterial(t *testing.T) {
	provider, err := NewLocalKeyProvider("2026a", map[string]string{
		"2026a": "the-current-key-value", "2025a": "the-older-key-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := provider.KeyIDs()
	if len(ids) != 2 || ids[0] != "2025a" || ids[1] != "2026a" {
		t.Fatalf("unexpected key ids: %v", ids)
	}
	if provider.KeyID() != "2026a" {
		t.Fatalf("wrong primary: %q", provider.KeyID())
	}
	for _, id := range ids {
		if strings.Contains(id, "key-value") {
			t.Fatal("key material leaked through the id listing")
		}
	}
}

func TestEnvConfigurationIsOptIn(t *testing.T) {
	t.Setenv("PROMTACT_ENCRYPTION_KEYS", "")
	provider, err := LocalKeyProviderFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if provider != nil {
		t.Fatal("encryption configured itself without being asked")
	}

	t.Setenv("PROMTACT_ENCRYPTION_KEYS", "2026a:the-current-key-value,2025a:the-older-key-value")
	t.Setenv("PROMTACT_ENCRYPTION_KEY_ID", "2026a")
	provider, err = LocalKeyProviderFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if provider == nil || provider.KeyID() != "2026a" || len(provider.KeyIDs()) != 2 {
		t.Fatalf("environment configuration was not applied: %+v", provider)
	}

	t.Setenv("PROMTACT_ENCRYPTION_KEYS", "missing-separator")
	if _, err := LocalKeyProviderFromEnv(); err == nil {
		t.Error("a malformed key list was accepted")
	}
}

func TestGeneratedKeysAreUsableAndDistinct(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 20; i++ {
		key, err := GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, dup := seen[key]; dup {
			t.Fatal("a key was generated twice")
		}
		seen[key] = struct{}{}
		if _, err := NewLocalKeyProvider("k", map[string]string{"k": key}); err != nil {
			t.Fatalf("a generated key was rejected by the provider: %v", err)
		}
	}
}
