package store

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestUsageMeteringIntegration proves the billing counter against a real
// Postgres: concurrent decisions must not lose increments, because a lost
// increment is silently unbilled revenue, and a double count is an overcharge.
func TestUsageMeteringIntegration(t *testing.T) {
	dsn := os.Getenv("PROMTACT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PROMTACT_TEST_POSTGRES_DSN to run Postgres integration tests")
	}

	s, err := NewWithPostgres(dsn)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	if s.SchemaVersion() < 6 {
		t.Fatalf("expected schema version >= 6 (usage metering), got %d", s.SchemaVersion())
	}

	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	tenant := "it-usage-" + suffix

	if _, err := s.CreateTenantAccount(ctx, TenantAccount{Tenant: tenant, DisplayName: "Usage"}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	defer func() {
		if db, err := s.directoryDB(); err == nil {
			_, _ = db.ExecContext(ctx, `DELETE FROM promtact_tenant_accounts WHERE tenant = $1`, tenant)
		}
	}()

	// Concurrent increments: every one must land exactly once.
	const workers = 16
	const each = 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := s.IncrementUsage(ctx, tenant, UsageToolDecisions, 1); err != nil {
					t.Errorf("increment: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	metrics, err := s.TenantUsage(ctx, tenant, time.Time{})
	if err != nil {
		t.Fatalf("read usage: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected exactly one metric row, got %+v", metrics)
	}
	if got, want := metrics[0].Quantity, int64(workers*each); got != want {
		t.Fatalf("lost or duplicated increments: counted %d, expected %d", got, want)
	}
	if metrics[0].Metric != UsageToolDecisions || metrics[0].Tenant != tenant {
		t.Fatalf("unexpected metric row: %+v", metrics[0])
	}

	// The period is the current UTC month, and a different month is separate.
	now := time.Now().UTC()
	wantPeriod := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if !metrics[0].PeriodStart.UTC().Equal(wantPeriod) {
		t.Fatalf("period start %s, expected %s", metrics[0].PeriodStart.UTC(), wantPeriod)
	}
	previous, err := s.TenantUsage(ctx, tenant, now.AddDate(0, -1, 0))
	if err != nil {
		t.Fatalf("read previous period: %v", err)
	}
	if len(previous) != 0 {
		t.Fatalf("a different billing period must be empty, got %+v", previous)
	}

	// Rejected inputs must not create rows.
	if err := s.IncrementUsage(ctx, tenant, UsageToolDecisions, 0); err == nil {
		t.Fatal("a non-positive quantity must be rejected")
	}
	if err := s.IncrementUsage(ctx, "", UsageToolDecisions, 1); err == nil {
		t.Fatal("an empty tenant must be rejected")
	}
}
