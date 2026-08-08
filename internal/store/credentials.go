package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// Storage for brokered tool credentials.
//
// These are the only secrets in this system that must be recoverable rather
// than merely verifiable. An API key can be stored as a hash, because the
// question is "does the presented value match". A tool credential has to be
// presented upstream, so the plaintext has to come back out.
//
// That makes the database a target it was not before: whoever can read the
// table can read every customer's upstream keys. So the value is sealed with
// the envelope encryption already used for MFA secrets, and the key lives
// outside the database. Without a key configured this refuses to store anything
// rather than writing plaintext - a silent downgrade here would put the
// customer's production credentials in every backup, and nothing would look
// wrong.

var errCredentialSealRequired = errors.New(
	"refusing to store a tool credential without envelope encryption configured: " +
		`set PROMTACT_ENCRYPTION_KEYS="<id>:<key>" and PROMTACT_ENCRYPTION_KEY_ID="<id>" and restart, ` +
		"otherwise the secret would sit in plaintext in every database backup")

// ErrCredentialSealRequired is returned when brokering is used on a deployment
// that has no encryption key.
func ErrCredentialSealRequired() error { return errCredentialSealRequired }

// SaveCredential stores or replaces a credential. The caller supplies the
// plaintext secret; it is sealed here and never written as given.
func (s *Store) SaveCredential(credential domain.Credential, secret string) (domain.Credential, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return domain.Credential{}, errors.New("credential secret is empty")
	}
	if strings.TrimSpace(credential.Tool) == "" {
		return domain.Credential{}, errors.New("credential tool pattern is empty")
	}

	sealer := s.currentSealer()
	if !sealer.Enabled() {
		return domain.Credential{}, errCredentialSealRequired
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sealed, err := sealer.Seal(ctx, secret)
	if err != nil {
		return domain.Credential{}, fmt.Errorf("sealing the credential failed: %w", err)
	}

	credential.Tenant = tenantOrEmpty(credential.Tenant)
	credential.Tool = strings.ToLower(strings.TrimSpace(credential.Tool))
	credential.Fingerprint = domain.CredentialFingerprint(secret)
	if credential.CreatedAt.IsZero() {
		credential.CreatedAt = time.Now().UTC()
	}
	// The plaintext is not kept on the returned value either. Callers that want
	// to use it have it already; callers that log the result should not.
	credential.Secret = ""

	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.credentials == nil {
			s.credentials = make(map[string]storedCredential)
		}
		s.credentials[credential.ID] = storedCredential{meta: credential, sealed: sealed}
		return credential, nil
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO promtact_tool_credentials
  (id, tenant, tool, header, scheme, secret_sealed, fingerprint, description, created_at, rotated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO UPDATE SET
  tool = EXCLUDED.tool,
  header = EXCLUDED.header,
  scheme = EXCLUDED.scheme,
  secret_sealed = EXCLUDED.secret_sealed,
  fingerprint = EXCLUDED.fingerprint,
  description = EXCLUDED.description,
  rotated_at = $11`,
		credential.ID, credential.Tenant, credential.Tool, credential.Header, credential.Scheme,
		sealed, credential.Fingerprint, credential.Description, credential.CreatedAt,
		credential.RotatedAt, time.Now().UTC())
	if err != nil {
		return domain.Credential{}, err
	}
	return credential, nil
}

// Credentials returns credential metadata without any secrets. This is what the
// API and console see.
func (s *Store) Credentials(tenant string) ([]domain.Credential, error) {
	loaded, err := s.loadCredentials(tenant, false)
	if err != nil {
		return nil, err
	}
	return loaded, nil
}

// CredentialsWithSecrets returns credentials with their plaintext unsealed, for
// the broker in the forwarding path. It is deliberately named so that a call
// site handing the result to anything user-facing reads as wrong.
func (s *Store) CredentialsWithSecrets(tenant string) ([]domain.Credential, error) {
	return s.loadCredentials(tenant, true)
}

func (s *Store) loadCredentials(tenant string, withSecrets bool) ([]domain.Credential, error) {
	tenant = tenantOrEmpty(tenant)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	unseal := func(meta domain.Credential, sealed string) (domain.Credential, error) {
		if !withSecrets {
			meta.Secret = ""
			return meta, nil
		}
		plain, err := s.currentSealer().Open(ctx, sealed)
		if err != nil {
			return domain.Credential{}, fmt.Errorf("opening credential %q failed: %w", meta.ID, err)
		}
		meta.Secret = plain
		return meta, nil
	}

	if s.db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		var loaded []domain.Credential
		for _, stored := range s.credentials {
			if !strings.EqualFold(stored.meta.Tenant, tenant) {
				continue
			}
			opened, err := unseal(stored.meta, stored.sealed)
			if err != nil {
				return nil, err
			}
			loaded = append(loaded, opened)
		}
		sort.Slice(loaded, func(i, j int) bool { return loaded[i].ID < loaded[j].ID })
		return loaded, nil
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT id, tenant, tool, header, scheme, secret_sealed, fingerprint,
       COALESCE(description, ''), created_at, rotated_at, last_used_at, use_count
FROM promtact_tool_credentials
WHERE tenant = $1
ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var loaded []domain.Credential
	for rows.Next() {
		var meta domain.Credential
		var sealed string
		if err := rows.Scan(&meta.ID, &meta.Tenant, &meta.Tool, &meta.Header, &meta.Scheme,
			&sealed, &meta.Fingerprint, &meta.Description, &meta.CreatedAt,
			&meta.RotatedAt, &meta.LastUsedAt, &meta.UseCount); err != nil {
			return nil, err
		}
		opened, err := unseal(meta, sealed)
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, opened)
	}
	return loaded, rows.Err()
}

// DeleteCredential removes a credential. Revoking access to a tool should not
// require touching the agent, which is half the point of holding the secret
// here in the first place.
func (s *Store) DeleteCredential(tenant string, id string) (bool, error) {
	tenant = tenantOrEmpty(tenant)
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		stored, ok := s.credentials[id]
		if !ok || !strings.EqualFold(stored.meta.Tenant, tenant) {
			return false, nil
		}
		delete(s.credentials, id)
		return true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := s.db.ExecContext(ctx,
		`DELETE FROM promtact_tool_credentials WHERE tenant = $1 AND id = $2`, tenant, id)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

// MarkCredentialUsed records that a credential was presented upstream. An
// unused credential is one nobody has revoked yet, and that is worth seeing.
func (s *Store) MarkCredentialUsed(tenant string, id string, at time.Time) {
	tenant = tenantOrEmpty(tenant)
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if stored, ok := s.credentials[id]; ok {
			used := at.UTC()
			stored.meta.LastUsedAt = &used
			stored.meta.UseCount++
			s.credentials[id] = stored
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Best effort by design: this is a usage statistic, and failing a tool call
	// because a counter could not be written would be the wrong trade.
	_, _ = s.db.ExecContext(ctx, `
UPDATE promtact_tool_credentials
SET last_used_at = $3, use_count = use_count + 1
WHERE tenant = $1 AND id = $2`, tenant, id, at.UTC())
}

type storedCredential struct {
	meta   domain.Credential
	sealed string
}
