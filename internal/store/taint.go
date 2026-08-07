package store

import (
	"context"
	"strings"
	"time"
)

// Session taint, kept where a restart cannot lose it.
//
// The mark saying a session has read untrusted content is a security control,
// not a statistic. Holding it only in the process meant that a deploy, a crash
// or an ordinary restart released every marked session at once — and nothing
// would have failed, logged, or looked different afterwards, which is the worst
// possible shape for a control to lose its state in.
//
// Writes happen on every tool result, so they are kept small and single-row.
// In memory mode there is nothing durable to write to and these are no-ops:
// the engine's own map is then the whole truth, and a restart legitimately
// starts from nothing.

type SessionTaint struct {
	Tenant    string
	Key       string
	Marks     []string
	TaintedAt time.Time
}

func (s *Store) SaveSessionTaint(tenant string, key string, marks []string, at time.Time) error {
	if s.db == nil || strings.TrimSpace(key) == "" || len(marks) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.ExecContext(ctx, `
INSERT INTO promtact_session_taint (tenant, session_key, marks, tainted_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant, session_key) DO UPDATE
SET marks = EXCLUDED.marks, tainted_at = EXCLUDED.tainted_at`,
		tenantOrEmpty(tenant), key, strings.Join(marks, "\n"), at.UTC())
	return err
}

// LoadSessionTaint returns the marks still inside the window. Anything older is
// expired by definition, so it is neither returned nor worth reading.
func (s *Store) LoadSessionTaint(since time.Time) ([]SessionTaint, error) {
	if s.db == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT tenant, session_key, marks, tainted_at
FROM promtact_session_taint
WHERE tainted_at >= $1`, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var loaded []SessionTaint
	for rows.Next() {
		var record SessionTaint
		var marks string
		if err := rows.Scan(&record.Tenant, &record.Key, &marks, &record.TaintedAt); err != nil {
			return nil, err
		}
		for _, mark := range strings.Split(marks, "\n") {
			if mark = strings.TrimSpace(mark); mark != "" {
				record.Marks = append(record.Marks, mark)
			}
		}
		if len(record.Marks) > 0 {
			loaded = append(loaded, record)
		}
	}
	return loaded, rows.Err()
}

// PruneSessionTaint removes marks that have expired. Without it the table grows
// with every session forever, and the rows are worthless the moment they fall
// outside the window.
func (s *Store) PruneSessionTaint(before time.Time) (int64, error) {
	if s.db == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := s.db.ExecContext(ctx,
		`DELETE FROM promtact_session_taint WHERE tainted_at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return affected, nil
}

func tenantOrEmpty(tenant string) string {
	if trimmed := strings.TrimSpace(tenant); trimmed != "" {
		return trimmed
	}
	return "default"
}
