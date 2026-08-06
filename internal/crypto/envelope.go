package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Envelope encryption for the few values that are secrets at rest rather than
// verifiers: a TOTP seed must be readable to check a code, so unlike an API key
// it cannot simply be hashed.
//
// Each record gets its own data key, which is used once and then wrapped by a
// key-encryption key that never touches the database. Rotating the outer key
// therefore means rewrapping small blobs rather than re-encrypting every row,
// and a database dump on its own decrypts nothing.
//
// The key-encryption key lives behind an interface so a deployment can hold it
// in a KMS or an HSM. A local provider ships for installations that have
// neither; it reads the key from the environment, which at least separates the
// key from the data — the property that matters when a backup leaks.
//
// AES-256-GCM comes from the standard library. Everything here is composition
// of primitives Go already provides, so no dependency is added to the path that
// protects the secrets.

const (
	envelopePrefix = "promtactenc.v1."
	dekSize        = 32 // AES-256
)

var (
	// ErrNoKey means encryption was requested without a key. It is fatal by
	// design: silently storing plaintext when an operator asked for encryption
	// is the failure they would never discover.
	ErrNoKey = errors.New("no key-encryption key is configured")

	// ErrUnknownKey means a record was wrapped by a key this process does not
	// have. Decryption must fail rather than skip the record, or a rotation
	// mistake shows up as users mysteriously losing their second factor.
	ErrUnknownKey = errors.New("the record was wrapped by an unknown key")
)

// KeyProvider wraps and unwraps data keys. An implementation may call out to a
// KMS; nothing here assumes the key material is ever present in this process.
type KeyProvider interface {
	// Wrap encrypts a data key and reports which key-encryption key was used.
	Wrap(ctx context.Context, dataKey []byte) (wrapped []byte, keyID string, err error)
	// Unwrap reverses Wrap for the named key-encryption key.
	Unwrap(ctx context.Context, wrapped []byte, keyID string) ([]byte, error)
	// KeyID names the key new records will be wrapped with.
	KeyID() string
}

// Sealer encrypts and decrypts field values. A nil Sealer is valid and passes
// values through unchanged, so an installation that has not configured
// encryption behaves exactly as before.
type Sealer struct {
	provider KeyProvider
}

func NewSealer(provider KeyProvider) *Sealer {
	if provider == nil {
		return nil
	}
	return &Sealer{provider: provider}
}

func (s *Sealer) Enabled() bool { return s != nil && s.provider != nil }

// Seal encrypts a value under a fresh data key and returns a self-describing
// string: the key id travels with the ciphertext so rotation does not require
// rewriting the whole table at once.
func (s *Sealer) Seal(ctx context.Context, plaintext string) (string, error) {
	if !s.Enabled() {
		return plaintext, nil
	}
	if plaintext == "" {
		return "", nil
	}

	dataKey := make([]byte, dekSize)
	if _, err := rand.Read(dataKey); err != nil {
		return "", err
	}
	ciphertext, err := aesSeal(dataKey, []byte(plaintext))
	if err != nil {
		return "", err
	}
	wrapped, keyID, err := s.provider.Wrap(ctx, dataKey)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(keyID, ".") {
		return "", fmt.Errorf("key id %q may not contain a dot", keyID)
	}

	return envelopePrefix + keyID + "." +
		base64.RawURLEncoding.EncodeToString(wrapped) + "." +
		base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// Open reverses Seal. A value that was never sealed is returned unchanged, so
// records written before encryption was enabled keep working and can be
// migrated lazily as they are rewritten.
func (s *Sealer) Open(ctx context.Context, stored string) (string, error) {
	if !strings.HasPrefix(stored, envelopePrefix) {
		return stored, nil
	}
	if !s.Enabled() {
		// Refusing here is the point: returning the ciphertext as if it were the
		// secret would make every second factor silently stop verifying.
		return "", ErrNoKey
	}

	parts := strings.SplitN(strings.TrimPrefix(stored, envelopePrefix), ".", 3)
	if len(parts) != 3 {
		return "", errors.New("the sealed value is malformed")
	}
	wrapped, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", err
	}
	dataKey, err := s.provider.Unwrap(ctx, wrapped, parts[0])
	if err != nil {
		return "", err
	}
	plaintext, err := aesOpen(dataKey, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// IsSealed reports whether a stored value carries an envelope. It lets a
// migration tell which rows still need rewriting without decrypting them.
func IsSealed(stored string) bool { return strings.HasPrefix(stored, envelopePrefix) }

func aesSeal(key []byte, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	// The nonce is prepended rather than stored separately; it is not secret,
	// and keeping it with the ciphertext removes any chance of the two being
	// paired up wrongly later.
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func aesOpen(key []byte, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("the ciphertext is too short")
	}
	nonce := ciphertext[:gcm.NonceSize()]
	return gcm.Open(nil, nonce, ciphertext[gcm.NonceSize():], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func readRandom(buf []byte) (int, error) { return rand.Read(buf) }
