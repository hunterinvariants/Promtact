package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const postgresTimeout = 10 * time.Second
const loginBackoffCap = 1 * time.Minute

func NewWithPostgres(dsn string) (*Store, error) {
	// The connection announces itself. Without it the access-log reconciler
	// cannot tell the application's own sessions from an operator's, and would
	// either alert on every ordinary query or on none.
	db, err := sql.Open("pgx", withApplicationName(dsn))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), postgresTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := New()
	s.db = db
	s.mode = "postgres"
	s.path = redactDSN(dsn)
	if err := s.postgresMigrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.postgresLoad(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *Store) postgresMigrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `SELECT pg_advisory_lock(72743001)`); err != nil {
		return err
	}
	defer s.db.ExecContext(context.Background(), `SELECT pg_advisory_unlock(72743001)`)

	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS promtact_schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`); err != nil {
		return err
	}

	applied, err := s.postgresAppliedMigrations(ctx)
	if err != nil {
		return err
	}
	for _, migration := range postgresMigrations {
		if applied[migration.Version] {
			continue
		}
		if err := s.applyPostgresMigration(ctx, migration); err != nil {
			return err
		}
		applied[migration.Version] = true
	}

	version, err := s.postgresCurrentSchemaVersion(ctx)
	if err != nil {
		return err
	}
	s.schemaVersion = version
	return nil
}

func (s *Store) postgresAppliedMigrations(ctx context.Context) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM promtact_schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

func (s *Store) applyPostgresMigration(ctx context.Context, migration postgresMigration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply postgres migration %d %s: %w", migration.Version, migration.Name, err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO promtact_schema_migrations (version, name)
VALUES ($1, $2)
ON CONFLICT (version) DO NOTHING`, migration.Version, migration.Name); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) postgresCurrentSchemaVersion(ctx context.Context) (int, error) {
	var version sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT max(version) FROM promtact_schema_migrations`).Scan(&version); err != nil {
		return 0, err
	}
	if !version.Valid {
		return 0, nil
	}
	return int(version.Int64), nil
}

type postgresMigration struct {
	Version int
	Name    string
	SQL     string
}

var postgresMigrations = []postgresMigration{
	{
		Version: 1,
		Name:    "initial_schema",
		SQL: `
CREATE TABLE IF NOT EXISTS promtact_events (
  id TEXT PRIMARY KEY,
  occurred_at TIMESTAMPTZ,
  asset_id TEXT,
  kind TEXT,
  data JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_promtact_events_occurred_at ON promtact_events (occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_promtact_events_asset_id ON promtact_events (asset_id);

CREATE TABLE IF NOT EXISTS promtact_alerts (
  id TEXT PRIMARY KEY,
  fingerprint TEXT UNIQUE,
  created_at TIMESTAMPTZ,
  asset_id TEXT,
  severity TEXT,
  status TEXT,
  data JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_promtact_alerts_created_at ON promtact_alerts (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_promtact_alerts_asset_id ON promtact_alerts (asset_id);
CREATE INDEX IF NOT EXISTS idx_promtact_alerts_status ON promtact_alerts (status);

CREATE TABLE IF NOT EXISTS promtact_actions (
  id TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ,
  asset_id TEXT,
  approval_status TEXT,
  data JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_promtact_actions_created_at ON promtact_actions (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_promtact_actions_asset_id ON promtact_actions (asset_id);

CREATE TABLE IF NOT EXISTS promtact_assets (
  id TEXT PRIMARY KEY,
  last_seen TIMESTAMPTZ,
  risk_score INTEGER,
  data JSONB NOT NULL
);

CREATE TABLE IF NOT EXISTS promtact_audit_events (
  id TEXT PRIMARY KEY,
  occurred_at TIMESTAMPTZ NOT NULL,
  actor TEXT,
  action TEXT NOT NULL,
  resource_type TEXT,
  resource_id TEXT,
  outcome TEXT,
  data JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_promtact_audit_events_occurred_at ON promtact_audit_events (occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_promtact_audit_events_actor ON promtact_audit_events (actor);
CREATE INDEX IF NOT EXISTS idx_promtact_audit_events_action ON promtact_audit_events (action);`,
	},
	{
		Version: 2,
		Name:    "audit_chain_state_and_login_attempts",
		SQL: `
CREATE TABLE IF NOT EXISTS promtact_audit_chain_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  head_hash TEXT NOT NULL DEFAULT '',
  chain_index INTEGER NOT NULL DEFAULT 0,
  valid BOOLEAN NOT NULL DEFAULT TRUE,
  anchor_hmac TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO promtact_audit_chain_state (id, head_hash, chain_index, valid)
VALUES (1, '', 0, TRUE)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS promtact_login_attempts (
  key TEXT PRIMARY KEY,
  failures INTEGER NOT NULL DEFAULT 0,
  blocked_until TIMESTAMPTZ,
  last_seen TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_promtact_login_attempts_blocked_until ON promtact_login_attempts (blocked_until DESC);
CREATE INDEX IF NOT EXISTS idx_promtact_login_attempts_last_seen ON promtact_login_attempts (last_seen DESC);`,
	},
	{
		Version: 3,
		Name:    "audit_chain_anchor_hmac",
		SQL: `
ALTER TABLE promtact_audit_chain_state
ADD COLUMN IF NOT EXISTS anchor_hmac TEXT NOT NULL DEFAULT '';`,
	},
	{
		Version: 4,
		Name:    "assets_tenant_scope",
		SQL: `
ALTER TABLE promtact_assets ADD COLUMN IF NOT EXISTS tenant TEXT NOT NULL DEFAULT 'default';
ALTER TABLE promtact_assets DROP CONSTRAINT IF EXISTS promtact_assets_pkey;
ALTER TABLE promtact_assets ADD PRIMARY KEY (tenant, id);
CREATE INDEX IF NOT EXISTS idx_promtact_assets_tenant ON promtact_assets (tenant);`,
	},
	{
		Version: 5,
		Name:    "tenant_accounts_users_api_keys",
		SQL: `
CREATE TABLE IF NOT EXISTS promtact_tenant_accounts (
  tenant TEXT PRIMARY KEY,
  display_name TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  plan TEXT NOT NULL DEFAULT 'standard',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS promtact_tenant_users (
  id TEXT PRIMARY KEY,
  tenant TEXT NOT NULL REFERENCES promtact_tenant_accounts (tenant) ON DELETE CASCADE,
  name TEXT NOT NULL,
  roles TEXT NOT NULL DEFAULT 'viewer',
  status TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Login resolves a principal by user name, so names must be globally unique:
-- an ambiguous name across tenants would make authentication non-deterministic.
CREATE UNIQUE INDEX IF NOT EXISTS idx_promtact_tenant_users_name ON promtact_tenant_users (lower(name));
CREATE INDEX IF NOT EXISTS idx_promtact_tenant_users_tenant ON promtact_tenant_users (tenant);

-- Only the SHA-256 of an API key is stored; the plaintext is shown once at
-- creation and is not recoverable afterwards.
CREATE TABLE IF NOT EXISTS promtact_api_keys (
  id TEXT PRIMARY KEY,
  tenant TEXT NOT NULL REFERENCES promtact_tenant_accounts (tenant) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES promtact_tenant_users (id) ON DELETE CASCADE,
  name TEXT NOT NULL DEFAULT '',
  token_sha256 TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_promtact_api_keys_tenant ON promtact_api_keys (tenant);
CREATE INDEX IF NOT EXISTS idx_promtact_api_keys_active ON promtact_api_keys (token_sha256) WHERE revoked_at IS NULL;`,
	},
	{
		Version: 6,
		Name:    "tenant_usage_metering",
		SQL: `
CREATE TABLE IF NOT EXISTS promtact_tenant_usage (
  tenant TEXT NOT NULL REFERENCES promtact_tenant_accounts (tenant) ON DELETE CASCADE,
  period_start DATE NOT NULL,
  metric TEXT NOT NULL,
  quantity BIGINT NOT NULL DEFAULT 0 CHECK (quantity >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant, period_start, metric)
);
CREATE INDEX IF NOT EXISTS idx_promtact_tenant_usage_period
ON promtact_tenant_usage (period_start DESC, tenant);`,
	},
	{
		Version: 7,
		Name:    "service_accounts_and_mfa",
		SQL: `
-- Humans and machines were previously the same kind of record, which makes a
-- second factor impossible to require: enforcing it would break every agent.
-- Existing rows become 'human' so behaviour does not change on upgrade.
ALTER TABLE promtact_tenant_users
  ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'human'
  CHECK (kind IN ('human', 'service'));

-- MFA is enabled per tenant. Until an operator turns it on nothing changes,
-- so upgrading cannot lock anyone out of their own console.
ALTER TABLE promtact_tenant_accounts
  ADD COLUMN IF NOT EXISTS mfa_required BOOLEAN NOT NULL DEFAULT false;

-- A TOTP secret is only usable once its owner has proven they can generate a
-- code from it, so confirmed_at gates enforcement rather than enrolment.
CREATE TABLE IF NOT EXISTS promtact_user_mfa (
  user_id TEXT PRIMARY KEY REFERENCES promtact_tenant_users (id) ON DELETE CASCADE,
  secret TEXT NOT NULL,
  confirmed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Replay defence: a TOTP code stays valid for its whole time step, so an
-- intercepted code must be refused for a second use within that window.
CREATE TABLE IF NOT EXISTS promtact_mfa_used_codes (
  user_id TEXT NOT NULL REFERENCES promtact_tenant_users (id) ON DELETE CASCADE,
  time_step BIGINT NOT NULL,
  used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, time_step)
);

-- Recovery codes are single-use and stored only as hashes, like API keys.
CREATE TABLE IF NOT EXISTS promtact_mfa_recovery_codes (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES promtact_tenant_users (id) ON DELETE CASCADE,
  code_sha256 TEXT NOT NULL,
  used_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_promtact_mfa_recovery_user
ON promtact_mfa_recovery_codes (user_id) WHERE used_at IS NULL;`,
	},
	{
		Version: 8,
		Name:    "session_taint",
		SQL: `
-- What an agent session has read, so a restart does not clear it.
--
-- The mark is what stands between a poisoned page and the action it was
-- planted to cause. Holding it only in memory meant a deploy, a crash or an
-- ordinary restart silently released every marked session at once - and the
-- release would look exactly like normal operation, because nothing fails.
--
-- Rows are small, short-lived and rewritten on every tool result, so this is
-- deliberately not part of the snapshot: it is its own table with its own
-- pruning.
CREATE TABLE IF NOT EXISTS promtact_session_taint (
  tenant TEXT NOT NULL,
  session_key TEXT NOT NULL,
  marks TEXT NOT NULL,
  tainted_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant, session_key)
);

-- Pruning is by age, so that is what is indexed.
CREATE INDEX IF NOT EXISTS idx_promtact_session_taint_age
ON promtact_session_taint (tainted_at);`,
	},
	{
		Version: 9,
		Name:    "tool_credentials",
		SQL: `
-- Tool credentials the gateway presents on an agent's behalf.
--
-- The agent holds a token this gateway accepts and nothing else does, so an
-- agent that finds a route around the gateway arrives at the tool without any
-- authority. Bypassing stops being a shortcut and becomes a dead end.
--
-- secret_sealed is envelope-encrypted with a key held outside this database,
-- because unlike an API key this value has to be recoverable to be useful, and
-- a readable table here would mean every customer's upstream keys in every
-- backup. The store refuses to write a row at all when no key is configured.
CREATE TABLE IF NOT EXISTS promtact_tool_credentials (
  id TEXT PRIMARY KEY,
  tenant TEXT NOT NULL,
  -- An exact tool name, a "prefix_*" wildcard, or "*" as the tenant fallback.
  tool TEXT NOT NULL,
  header TEXT NOT NULL DEFAULT '',
  scheme TEXT NOT NULL DEFAULT '',
  secret_sealed TEXT NOT NULL,
  -- A short digest, so an operator can confirm which secret is installed and
  -- that a rotation replaced it, without any API that reads the value back.
  fingerprint TEXT NOT NULL,
  description TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  rotated_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  use_count BIGINT NOT NULL DEFAULT 0
);

-- Selection happens per tool call, in the latency path, and always by tenant.
CREATE INDEX IF NOT EXISTS idx_promtact_tool_credentials_tenant
ON promtact_tool_credentials (tenant);`,
	},
	{
		Version: 10,
		Name:    "witness_receipts",
		SQL: `
-- Signed statements from an external witness about what it saw, and when.
--
-- Publishing a head to a witness already turns rewritten history into a
-- disagreement. But noticing the disagreement means asking the witness, and
-- that answer is only as good as the witness being reachable and honest at the
-- moment the question is asked. A stored receipt can be checked offline, by
-- anyone holding the public key, without the witness and without trusting the
-- operator who handed over the database.
--
-- The consequence worth having is about absence: every witnessed record has a
-- receipt, so a range without one was never witnessed. That turns a gap from
-- "no evidence" into evidence.
--
-- Deliberately outside retention. Every other table here ages out; deleting the
-- proof that a range was witnessed would reopen the hole this fills, on a
-- schedule, automatically.
CREATE TABLE IF NOT EXISTS promtact_witness_receipts (
  chain_index BIGINT PRIMARY KEY,
  head TEXT NOT NULL,
  -- Stored as text, exactly as the witness sent it. A timestamptz round trip
  -- renormalises precision, and the signature is over the original characters:
  -- reformatting the timestamp turns a valid receipt into a forgery report.
  witnessed_at TEXT NOT NULL,
  -- Empty for a witness that predates receipt signing. An unsigned receipt is
  -- reported as unsigned rather than as valid or as a failure.
  signature TEXT,
  key_id TEXT,
  stored_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`,
	},
}

func (s *Store) postgresLoad(ctx context.Context) error {
	if err := s.postgresLoadEvents(ctx); err != nil {
		return err
	}
	if err := s.postgresLoadAlerts(ctx); err != nil {
		return err
	}
	if err := s.postgresLoadActions(ctx); err != nil {
		return err
	}
	if err := s.postgresLoadAssets(ctx); err != nil {
		return err
	}
	if err := s.postgresLoadAudits(ctx); err != nil {
		return err
	}
	s.rebuildFingerprintsLocked()
	if len(s.assets) == 0 {
		s.rebuildAssetsLocked()
	}
	s.rebuildAuditChainLocked()
	if err := s.postgresSyncAuditChainState(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) postgresLoadEvents(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM promtact_events ORDER BY occurred_at ASC NULLS LAST, id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return err
		}
		var event domain.Event
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		s.events = append(s.events, event)
	}
	return rows.Err()
}

func (s *Store) postgresLoadAlerts(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM promtact_alerts ORDER BY created_at ASC NULLS LAST, id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return err
		}
		var alert domain.Alert
		if err := json.Unmarshal(data, &alert); err != nil {
			return err
		}
		s.alerts = append(s.alerts, alert)
	}
	return rows.Err()
}

func (s *Store) postgresLoadActions(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM promtact_actions ORDER BY created_at ASC NULLS LAST, id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return err
		}
		var action domain.ResponseAction
		if err := json.Unmarshal(data, &action); err != nil {
			return err
		}
		s.actions = append(s.actions, action)
	}
	return rows.Err()
}

func (s *Store) postgresLoadAssets(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT tenant, data FROM promtact_assets`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tenant string
		var data []byte
		if err := rows.Scan(&tenant, &data); err != nil {
			return err
		}
		var asset domain.Asset
		if err := json.Unmarshal(data, &asset); err != nil {
			return err
		}
		if asset.Tenant == "" {
			asset.Tenant = tenantOrDefault(tenant)
		}
		if asset.ID != "" {
			s.assets[assetKey(asset.Tenant, asset.ID)] = asset
		}
	}
	return rows.Err()
}

func (s *Store) postgresLoadAudits(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM promtact_audit_events ORDER BY occurred_at ASC, id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return err
		}
		var event domain.AuditEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		s.audits = append(s.audits, event)
	}
	return rows.Err()
}

func (s *Store) postgresSyncAuditChainState(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO promtact_audit_chain_state (id, head_hash, chain_index, valid, anchor_hmac, updated_at)
VALUES (1, $1, $2, $3, $4, now())
ON CONFLICT (id) DO UPDATE SET
  head_hash = EXCLUDED.head_hash,
  chain_index = EXCLUDED.chain_index,
  valid = EXCLUDED.valid,
  anchor_hmac = EXCLUDED.anchor_hmac,
  updated_at = EXCLUDED.updated_at`,
		s.auditChainHead, len(s.audits), s.auditChainValid, s.auditChainAnchor); err != nil {
		return err
	}
	return nil
}

func (s *Store) postgresAddAuditLocked(event domain.AuditEvent) (domain.AuditEvent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), postgresTimeout)
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	// Concurrent audit writers are serialized by the SELECT ... FOR UPDATE on the
	// singleton chain-state row below. A session-level pg_advisory_lock here is
	// both redundant and unsafe: the tx-deferred pg_advisory_unlock runs after
	// Commit (ErrTxDone) and leaks the global lock, which then blocks the next
	// NewWithPostgres migration on the same lock id.
	if _, err := tx.ExecContext(ctx, `
INSERT INTO promtact_audit_chain_state (id, head_hash, chain_index, valid, anchor_hmac, updated_at)
VALUES (1, '', 0, TRUE, '', now())
ON CONFLICT (id) DO NOTHING`); err != nil {
		_ = tx.Rollback()
		return domain.AuditEvent{}, err
	}

	var headHash string
	var chainIndex int
	if err := tx.QueryRowContext(ctx, `SELECT head_hash, chain_index FROM promtact_audit_chain_state WHERE id = 1 FOR UPDATE`).Scan(&headHash, &chainIndex); err != nil {
		_ = tx.Rollback()
		return domain.AuditEvent{}, err
	}

	event.ChainIndex = chainIndex + 1
	event.PrevHash = headHash
	event.Hash = auditEventHash(event, event.PrevHash)
	data, err := json.Marshal(event)
	if err != nil {
		_ = tx.Rollback()
		return domain.AuditEvent{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO promtact_audit_events (id, occurred_at, actor, action, resource_type, resource_id, outcome, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET
  occurred_at = EXCLUDED.occurred_at,
  actor = EXCLUDED.actor,
  action = EXCLUDED.action,
  resource_type = EXCLUDED.resource_type,
  resource_id = EXCLUDED.resource_id,
  outcome = EXCLUDED.outcome,
  data = EXCLUDED.data`,
		event.ID, event.Timestamp, event.Actor, event.Action, event.ResourceType, event.ResourceID, event.Outcome, data); err != nil {
		_ = tx.Rollback()
		return domain.AuditEvent{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE promtact_audit_chain_state
SET head_hash = $1, chain_index = $2, valid = TRUE, anchor_hmac = $3, updated_at = now()
WHERE id = 1`, event.Hash, event.ChainIndex, auditChainAnchorValue(event.Hash, event.ChainIndex, true)); err != nil {
		_ = tx.Rollback()
		return domain.AuditEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.AuditEvent{}, err
	}
	s.auditChainHead = event.Hash
	s.auditChainValid = true
	s.auditChainAnchor = auditChainAnchorValue(event.Hash, event.ChainIndex, true)
	return event, nil
}

func (s *Store) postgresAuditChainSnapshot() AuditChainSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), postgresTimeout)
	defer cancel()

	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return AuditChainSnapshot{}
	}

	// The stored head/anchor are the tamper-evident seal. Chain validity is
	// re-derived from the event rows below (recompute every record hash and walk
	// the PrevHash links) so DB tampering of a non-head record is detected at
	// runtime instead of trusting the stored `valid` flag.
	var storedHead, storedAnchor sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT head_hash, anchor_hmac FROM promtact_audit_chain_state WHERE id = 1`).Scan(&storedHead, &storedAnchor); err != nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.auditChainSnapshotLocked()
	}

	rows, err := db.QueryContext(ctx, `SELECT id, occurred_at, data FROM promtact_audit_events ORDER BY (data->>'chain_index')::int ASC NULLS LAST, occurred_at ASC, id ASC`)
	if err != nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.auditChainSnapshotLocked()
	}
	defer rows.Close()

	snap := AuditChainSnapshot{Valid: true}
	previous := ""
	for rows.Next() {
		var id string
		var ts time.Time
		var data []byte
		if err := rows.Scan(&id, &ts, &data); err != nil {
			snap.Valid = false
			break
		}
		snap.Total++
		var event domain.AuditEvent
		if err := json.Unmarshal(data, &event); err != nil {
			snap.Valid = false
			continue
		}
		if strings.TrimSpace(event.Hash) == "" {
			continue
		}
		snap.Linked++
		if event.PrevHash != previous {
			snap.Valid = false
		}
		if event.Hash != auditEventHash(event, event.PrevHash) {
			snap.Valid = false
		}
		previous = event.Hash
		snap.Head = event.Hash
		snap.Previous = event.PrevHash
		snap.LastAuditID = id
		snap.LastTimestamp = ts
	}
	if err := rows.Err(); err != nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.auditChainSnapshotLocked()
	}

	if snap.Head == "" {
		snap.Valid = snap.Total == 0
		snap.Anchored = false
		return snap
	}

	// The re-derived head must match the stored head, and the anchor recomputed
	// over the re-derived (head, linked, valid) must match the stored anchor.
	if storedHead.Valid && strings.TrimSpace(storedHead.String) != "" && storedHead.String != snap.Head {
		snap.Valid = false
	}
	expectedAnchor := auditChainAnchorValue(snap.Head, snap.Linked, snap.Valid)
	snap.Anchor = expectedAnchor
	snap.Anchored = expectedAnchor != ""
	if storedAnchor.Valid && strings.TrimSpace(storedAnchor.String) != "" && expectedAnchor != "" && storedAnchor.String != expectedAnchor {
		snap.Valid = false
	}
	return snap
}

func (s *Store) postgresLoginRetryAfter(key string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), postgresTimeout)
	defer cancel()
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, nil
	}
	var blockedUntil sql.NullTime
	if err := s.db.QueryRowContext(ctx, `SELECT blocked_until FROM promtact_login_attempts WHERE key = $1`, key).Scan(&blockedUntil); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	if !blockedUntil.Valid {
		return 0, nil
	}
	wait := time.Until(blockedUntil.Time)
	if wait < 0 {
		return 0, nil
	}
	return wait, nil
}

func (s *Store) postgresRecordLoginAttempt(key string, success bool) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), postgresTimeout)
	defer cancel()
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, nil
	}
	now := time.Now().UTC()
	if success {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM promtact_login_attempts WHERE key = $1`, key); err != nil {
			return 0, err
		}
		return 0, nil
	}
	var failures int
	err := s.db.QueryRowContext(ctx, `SELECT failures FROM promtact_login_attempts WHERE key = $1`, key).Scan(&failures)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if failures < 0 {
		failures = 0
	}
	failures++
	if failures > 8 {
		failures = 8
	}
	delay := time.Second << (failures - 1)
	if delay > loginBackoffCap {
		delay = loginBackoffCap
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO promtact_login_attempts (key, failures, blocked_until, last_seen)
VALUES ($1, $2, $3, $4)
ON CONFLICT (key) DO UPDATE SET
  failures = EXCLUDED.failures,
  blocked_until = EXCLUDED.blocked_until,
  last_seen = EXCLUDED.last_seen`,
		key, failures, now.Add(delay), now)
	return delay, err
}

func (s *Store) postgresPersistEventLocked(event domain.Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), postgresTimeout)
	defer cancel()
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO promtact_events (id, occurred_at, asset_id, kind, data)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
  occurred_at = EXCLUDED.occurred_at,
  asset_id = EXCLUDED.asset_id,
  kind = EXCLUDED.kind,
  data = EXCLUDED.data`,
		event.ID, nullableTime(event.Timestamp), event.AssetID, string(event.Kind), data); err != nil {
		return err
	}
	return s.postgresPersistAssetsLocked(ctx)
}

func postgresInsertEventsTx(ctx context.Context, tx *sql.Tx, events []domain.Event) error {
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO promtact_events (id, occurred_at, asset_id, kind, data)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
  occurred_at = EXCLUDED.occurred_at,
  asset_id = EXCLUDED.asset_id,
  kind = EXCLUDED.kind,
  data = EXCLUDED.data`,
			event.ID, nullableTime(event.Timestamp), event.AssetID, string(event.Kind), data); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) postgresPersistAlertsLocked(alerts []domain.Alert) error {
	ctx, cancel := context.WithTimeout(context.Background(), postgresTimeout)
	defer cancel()
	for _, alert := range alerts {
		data, err := json.Marshal(alert)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO promtact_alerts (id, fingerprint, created_at, asset_id, severity, status, data)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
  fingerprint = EXCLUDED.fingerprint,
  created_at = EXCLUDED.created_at,
  asset_id = EXCLUDED.asset_id,
  severity = EXCLUDED.severity,
  status = EXCLUDED.status,
  data = EXCLUDED.data`,
			alert.ID, nullEmpty(alert.Fingerprint), nullableTime(alert.CreatedAt), alert.AssetID, string(alert.Severity), string(alert.Status), data); err != nil {
			return err
		}
	}
	return s.postgresPersistAssetsLocked(ctx)
}

func postgresInsertAlertsTx(ctx context.Context, tx *sql.Tx, alerts []domain.Alert) error {
	for _, alert := range alerts {
		data, err := json.Marshal(alert)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO promtact_alerts (id, fingerprint, created_at, asset_id, severity, status, data)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
  fingerprint = EXCLUDED.fingerprint,
  created_at = EXCLUDED.created_at,
  asset_id = EXCLUDED.asset_id,
  severity = EXCLUDED.severity,
  status = EXCLUDED.status,
  data = EXCLUDED.data`,
			alert.ID, nullEmpty(alert.Fingerprint), nullableTime(alert.CreatedAt), alert.AssetID, string(alert.Severity), string(alert.Status), data); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) postgresPersistActionsLocked(actions []domain.ResponseAction) error {
	ctx, cancel := context.WithTimeout(context.Background(), postgresTimeout)
	defer cancel()
	for _, action := range actions {
		data, err := json.Marshal(action)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO promtact_actions (id, created_at, asset_id, approval_status, data)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
  created_at = EXCLUDED.created_at,
  asset_id = EXCLUDED.asset_id,
  approval_status = EXCLUDED.approval_status,
  data = EXCLUDED.data`,
			action.ID, nullableTime(action.CreatedAt), action.AssetID, action.ApprovalStatus, data); err != nil {
			return err
		}
	}
	return nil
}

func postgresInsertActionsTx(ctx context.Context, tx *sql.Tx, actions []domain.ResponseAction) error {
	for _, action := range actions {
		data, err := json.Marshal(action)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO promtact_actions (id, created_at, asset_id, approval_status, data)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
  created_at = EXCLUDED.created_at,
  asset_id = EXCLUDED.asset_id,
  approval_status = EXCLUDED.approval_status,
  data = EXCLUDED.data`,
			action.ID, nullableTime(action.CreatedAt), action.AssetID, action.ApprovalStatus, data); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) postgresPersistAuditLocked(event domain.AuditEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), postgresTimeout)
	defer cancel()
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO promtact_audit_events (id, occurred_at, actor, action, resource_type, resource_id, outcome, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET
  occurred_at = EXCLUDED.occurred_at,
  actor = EXCLUDED.actor,
  action = EXCLUDED.action,
  resource_type = EXCLUDED.resource_type,
  resource_id = EXCLUDED.resource_id,
  outcome = EXCLUDED.outcome,
  data = EXCLUDED.data`,
		event.ID, event.Timestamp, event.Actor, event.Action, event.ResourceType, event.ResourceID, event.Outcome, data)
	return err
}

func postgresInsertAuditsTx(ctx context.Context, tx *sql.Tx, audits []domain.AuditEvent) error {
	for _, event := range audits {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO promtact_audit_events (id, occurred_at, actor, action, resource_type, resource_id, outcome, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET
  occurred_at = EXCLUDED.occurred_at,
  actor = EXCLUDED.actor,
  action = EXCLUDED.action,
  resource_type = EXCLUDED.resource_type,
  resource_id = EXCLUDED.resource_id,
  outcome = EXCLUDED.outcome,
  data = EXCLUDED.data`,
			event.ID, event.Timestamp, event.Actor, event.Action, event.ResourceType, event.ResourceID, event.Outcome, data); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) postgresPersistAssetsLocked(ctx context.Context) error {
	for _, asset := range s.assets {
		data, err := json.Marshal(asset)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO promtact_assets (tenant, id, last_seen, risk_score, data)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant, id) DO UPDATE SET
  last_seen = EXCLUDED.last_seen,
  risk_score = EXCLUDED.risk_score,
  data = EXCLUDED.data`,
			tenantOrDefault(asset.Tenant), asset.ID, nullableTime(asset.LastSeen), asset.RiskScore, data); err != nil {
			return err
		}
	}
	return nil
}

func postgresInsertAssetsTx(ctx context.Context, tx *sql.Tx, assets map[string]domain.Asset) error {
	for _, asset := range assets {
		data, err := json.Marshal(asset)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO promtact_assets (tenant, id, last_seen, risk_score, data)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant, id) DO UPDATE SET
  last_seen = EXCLUDED.last_seen,
  risk_score = EXCLUDED.risk_score,
  data = EXCLUDED.data`,
			tenantOrDefault(asset.Tenant), asset.ID, nullableTime(asset.LastSeen), asset.RiskScore, data); err != nil {
			return err
		}
	}
	return nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func nullEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func redactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	return "postgres"
}

// withApplicationName adds application_name to a DSN unless the caller already
// set one, so an operator can still override it deliberately.
func withApplicationName(dsn string) string {
	if strings.Contains(dsn, "application_name=") {
		return dsn
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "application_name=promtact"
}
