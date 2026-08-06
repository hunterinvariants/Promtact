package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// A deployment created before the project was renamed carries the former table
// prefix. If the rename did not happen before the migration ledger is read, the
// new code would find no ledger, conclude the database is empty and create a
// fresh schema beside the real data — indistinguishable from total data loss
// for whoever is on call.
//
// This builds that exact situation in a throwaway schema and proves the data
// survives.
func TestLegacyTablePrefixIsMigratedWithoutLosingData(t *testing.T) {
	dsn := os.Getenv("PROMTACT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PROMTACT_TEST_POSTGRES_DSN to run Postgres integration tests")
	}
	s, err := NewWithPostgres(dsn)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	schema := "legacy_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")

	// Everything happens in its own schema so the live tables are never at risk.
	if _, err := s.db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	})
	if _, err := s.db.ExecContext(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	defer func() { _, _ = s.db.ExecContext(context.Background(), `SET search_path TO public`) }()

	// Stand up a miniature of the old schema, with a row that must survive.
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE oatd_schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now());
INSERT INTO oatd_schema_migrations (version, name) VALUES (1, 'initial_schema'), (7, 'service_accounts_and_mfa');
CREATE TABLE oatd_tenant_accounts (tenant TEXT PRIMARY KEY, display_name TEXT NOT NULL DEFAULT '');
INSERT INTO oatd_tenant_accounts (tenant, display_name) VALUES ('acme', 'Acme Corp');
CREATE INDEX idx_oatd_tenant_accounts_display ON oatd_tenant_accounts (display_name);`); err != nil {
		t.Fatalf("build legacy schema: %v", err)
	}

	if err := s.migrateLegacyTablePrefix(ctx); err != nil {
		t.Fatalf("migrate legacy prefix: %v", err)
	}

	// The ledger must carry over with its applied versions intact. Losing it
	// would make every migration run again against populated tables.
	var applied int
	if err := s.db.QueryRowContext(ctx,
		`SELECT max(version) FROM promtact_schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("the migration ledger did not survive the rename: %v", err)
	}
	if applied != 7 {
		t.Fatalf("expected the ledger to still report version 7, got %d", applied)
	}

	var display string
	if err := s.db.QueryRowContext(ctx,
		`SELECT display_name FROM promtact_tenant_accounts WHERE tenant = 'acme'`).Scan(&display); err != nil {
		t.Fatalf("tenant data did not survive the rename: %v", err)
	}
	if display != "Acme Corp" {
		t.Fatalf("tenant data was altered: %q", display)
	}

	// Indexes carry the prefix too; leaving them behind would collide the next
	// time a migration tries to create one under the new name.
	var indexes int
	if err := s.db.QueryRowContext(ctx, `
SELECT count(*) FROM pg_indexes
WHERE schemaname = $1 AND indexname LIKE 'idx\_promtact\_%'`, schema).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 1 {
		t.Fatalf("expected the index to be renamed, found %d", indexes)
	}

	// Nothing may be left under the old prefix.
	var leftovers int
	if err := s.db.QueryRowContext(ctx, `
SELECT count(*) FROM pg_tables WHERE schemaname = $1 AND tablename LIKE 'oatd\_%'`, schema).Scan(&leftovers); err != nil {
		t.Fatal(err)
	}
	if leftovers != 0 {
		t.Fatalf("%d tables still carry the old prefix", leftovers)
	}

	// Running it again must do nothing rather than fail: startup calls it on
	// every boot.
	if err := s.migrateLegacyTablePrefix(ctx); err != nil {
		t.Fatalf("a second run failed: %v", err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT display_name FROM promtact_tenant_accounts WHERE tenant = 'acme'`).Scan(&display); err != nil || display != "Acme Corp" {
		t.Fatalf("the second run disturbed the data: %q %v", display, err)
	}
}

// A fresh installation has no legacy tables, and the rename must not touch
// anything or fail.
func TestLegacyPrefixMigrationIsNoOpOnAFreshDatabase(t *testing.T) {
	dsn := os.Getenv("PROMTACT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PROMTACT_TEST_POSTGRES_DSN to run Postgres integration tests")
	}
	s, err := NewWithPostgres(dsn)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	schema := "fresh_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	if _, err := s.db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	})
	if _, err := s.db.ExecContext(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = s.db.ExecContext(context.Background(), `SET search_path TO public`) }()

	if err := s.migrateLegacyTablePrefix(ctx); err != nil {
		t.Fatalf("the rename failed on a database that has nothing to rename: %v", err)
	}
}
