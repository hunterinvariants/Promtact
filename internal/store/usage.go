package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const UsageToolDecisions = "tool_decisions"

type UsageMetric struct {
	Tenant      string    `json:"tenant"`
	PeriodStart time.Time `json:"period_start"`
	Metric      string    `json:"metric"`
	Quantity    int64     `json:"quantity"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Store) IncrementUsage(ctx context.Context, tenant, metric string, quantity int64) error {
	db, err := s.directoryDB()
	if err != nil {
		return err
	}
	tenant = strings.ToLower(strings.TrimSpace(tenant))
	metric = strings.ToLower(strings.TrimSpace(metric))
	if tenant == "" || metric == "" || quantity <= 0 {
		return errors.New("tenant, metric and a positive quantity are required")
	}
	_, err = db.ExecContext(ctx, `INSERT INTO promtact_tenant_usage (tenant, period_start, metric, quantity)
VALUES ($1, date_trunc('month', now() AT TIME ZONE 'UTC')::date, $2, $3)
ON CONFLICT (tenant, period_start, metric) DO UPDATE
SET quantity = promtact_tenant_usage.quantity + EXCLUDED.quantity, updated_at = now()`, tenant, metric, quantity)
	return err
}

func (s *Store) TenantUsage(ctx context.Context, tenant string, period time.Time) ([]UsageMetric, error) {
	db, err := s.directoryDB()
	if err != nil {
		return nil, err
	}
	tenant = strings.ToLower(strings.TrimSpace(tenant))
	if tenant == "" {
		return nil, errors.New("tenant is required")
	}
	if period.IsZero() {
		period = time.Now().UTC()
	}
	period = time.Date(period.UTC().Year(), period.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	rows, err := db.QueryContext(ctx, `SELECT tenant, period_start, metric, quantity, updated_at FROM promtact_tenant_usage WHERE tenant = $1 AND period_start = $2 ORDER BY metric`, tenant, period)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	metrics := []UsageMetric{}
	for rows.Next() {
		var metric UsageMetric
		if err := rows.Scan(&metric.Tenant, &metric.PeriodStart, &metric.Metric, &metric.Quantity, &metric.UpdatedAt); err != nil {
			return nil, err
		}
		metrics = append(metrics, metric)
	}
	return metrics, rows.Err()
}

func (s *Store) DatabaseStats() (sql.DBStats, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return sql.DBStats{}, false
	}
	return s.db.Stats(), true
}
