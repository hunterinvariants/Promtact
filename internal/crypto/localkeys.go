package crypto

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// LocalKeyProvider holds key-encryption keys in process memory, read from the
// environment at startup.
//
// This is the provider for installations without a KMS. It is deliberately not
// the only one: the KeyProvider interface exists so a deployment can move the
// wrapping operation into a KMS or HSM without touching any of the code that
// reads or writes secrets. What the local provider buys even so is separation —
// the keys are not in the database, so a leaked dump or a stolen backup
// decrypts nothing on its own.
//
// It holds several keys at once. Rotation adds a new primary while the old keys
// stay available for unwrapping, so records can be rewritten gradually instead
// of in one migration that must not fail halfway.

type LocalKeyProvider struct {
	keys    map[string][]byte
	primary string
}

// NewLocalKeyProvider builds a provider from id/secret pairs. The primary key
// is the one new records are wrapped with; every key remains usable to unwrap.
func NewLocalKeyProvider(primary string, keys map[string]string) (*LocalKeyProvider, error) {
	if len(keys) == 0 {
		return nil, ErrNoKey
	}
	provider := &LocalKeyProvider{keys: make(map[string][]byte, len(keys))}
	for id, secret := range keys {
		id = strings.TrimSpace(id)
		secret = strings.TrimSpace(secret)
		if id == "" || secret == "" {
			return nil, errors.New("every key needs an id and a value")
		}
		if strings.Contains(id, ".") {
			return nil, fmt.Errorf("key id %q may not contain a dot", id)
		}
		if len(secret) < 16 {
			// A short key here is worse than none, because it produces the
			// appearance of encryption at rest without the substance.
			return nil, fmt.Errorf("key %q is too short; use at least 16 characters", id)
		}
		// The configured value is a passphrase of arbitrary length, so it is
		// hashed to the fixed width AES needs rather than being truncated.
		sum := sha256.Sum256([]byte(secret))
		provider.keys[id] = sum[:]
	}

	primary = strings.TrimSpace(primary)
	if primary == "" {
		if len(keys) != 1 {
			return nil, errors.New("with several keys configured, the primary must be named")
		}
		for id := range provider.keys {
			primary = id
		}
	}
	if _, ok := provider.keys[primary]; !ok {
		return nil, fmt.Errorf("the primary key %q is not among the configured keys", primary)
	}
	provider.primary = primary
	return provider, nil
}

// LocalKeyProviderFromEnv reads PROMTACT_ENCRYPTION_KEYS and PROMTACT_ENCRYPTION_KEY_ID.
//
// The keys are given as id:value pairs separated by commas, so an operator can
// introduce a new key before switching to it:
//
//	PROMTACT_ENCRYPTION_KEYS="2026a:<value>,2025a:<older value>"
//	PROMTACT_ENCRYPTION_KEY_ID="2026a"
//
// It returns nil without error when nothing is configured: encryption at rest
// is opt-in, and an installation that has not asked for it must keep working.
func LocalKeyProviderFromEnv() (*LocalKeyProvider, error) {
	raw := strings.TrimSpace(os.Getenv("PROMTACT_ENCRYPTION_KEYS"))
	if raw == "" {
		return nil, nil
	}
	keys := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		id, value, found := strings.Cut(entry, ":")
		if !found {
			return nil, errors.New(`PROMTACT_ENCRYPTION_KEYS entries must be "id:value"`)
		}
		keys[strings.TrimSpace(id)] = strings.TrimSpace(value)
	}
	return NewLocalKeyProvider(os.Getenv("PROMTACT_ENCRYPTION_KEY_ID"), keys)
}

func (p *LocalKeyProvider) KeyID() string { return p.primary }

// KeyIDs lists every key that can unwrap, for operational visibility during a
// rotation. It never exposes key material.
func (p *LocalKeyProvider) KeyIDs() []string {
	ids := make([]string, 0, len(p.keys))
	for id := range p.keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (p *LocalKeyProvider) Wrap(_ context.Context, dataKey []byte) ([]byte, string, error) {
	key, ok := p.keys[p.primary]
	if !ok {
		return nil, "", ErrNoKey
	}
	wrapped, err := aesSeal(key, dataKey)
	if err != nil {
		return nil, "", err
	}
	return wrapped, p.primary, nil
}

func (p *LocalKeyProvider) Unwrap(_ context.Context, wrapped []byte, keyID string) ([]byte, error) {
	key, ok := p.keys[strings.TrimSpace(keyID)]
	if !ok {
		// Naming the missing key is what makes a botched rotation diagnosable;
		// the failure would otherwise look like data corruption.
		return nil, fmt.Errorf("%w: %q", ErrUnknownKey, keyID)
	}
	return aesOpen(key, wrapped)
}

// GenerateKey returns a value suitable for PROMTACT_ENCRYPTION_KEYS.
func GenerateKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := readRandom(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
