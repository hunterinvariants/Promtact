package store

import (
	"context"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// Removing a decommissioned asset and everything recorded about it.
//
// Until this existed there was no way to do it through the application at all,
// and the only route was SQL against the database by hand. That is wrong twice
// over. A customer cannot reach the database, so "we retired that laptop" had
// no answer. And an operator who does reach it deletes rows underneath a
// running process that holds the same records in memory and writes them back —
// so the deletion appears to do nothing until a restart, and can be undone by
// one.
//
// Memory and storage are therefore cleared together, under the same lock.

// RemovedCounts reports what was removed, so a caller can say what happened
// rather than "done".
type RemovedCounts struct {
	Events  int
	Alerts  int
	Actions int
	Assets  int
}

func (c RemovedCounts) Total() int {
	return c.Events + c.Alerts + c.Actions + c.Assets
}

// RemoveAsset deletes an asset and its events, alerts and response actions.
//
// Audit records are deliberately untouched. They are hash-chained, and a chain
// that can have entries taken out of it is not a chain — the record that these
// things once existed has to outlive the things themselves.
func (s *Store) RemoveAsset(tenant string, assetID string) (RemovedCounts, error) {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return RemovedCounts{}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var counts RemovedCounts
	matches := func(candidateTenant, candidateAsset string) bool {
		if !strings.EqualFold(strings.TrimSpace(candidateAsset), assetID) {
			return false
		}
		// An empty tenant on the request means the caller is not scoping by
		// tenant; an empty tenant on the record means it predates tenancy.
		if strings.TrimSpace(tenant) == "" || strings.TrimSpace(candidateTenant) == "" {
			return true
		}
		return strings.EqualFold(strings.TrimSpace(candidateTenant), strings.TrimSpace(tenant))
	}

	keptEvents := s.events[:0:0]
	for _, event := range s.events {
		if matches(event.Tenant, event.AssetID) {
			counts.Events++
			continue
		}
		keptEvents = append(keptEvents, event)
	}
	s.events = keptEvents

	keptAlerts := s.alerts[:0:0]
	for _, alert := range s.alerts {
		if matches(alert.Tenant, alert.AssetID) {
			counts.Alerts++
			// Drop the fingerprint too, or the same finding can never be raised
			// again on a machine that later comes back under the same name.
			delete(s.fingerprints, alert.Fingerprint)
			continue
		}
		keptAlerts = append(keptAlerts, alert)
	}
	s.alerts = keptAlerts

	keptActions := s.actions[:0:0]
	for _, action := range s.actions {
		if matches(action.Tenant, action.AssetID) {
			counts.Actions++
			continue
		}
		keptActions = append(keptActions, action)
	}
	s.actions = keptActions

	for key, asset := range s.assets {
		if matches(asset.Tenant, asset.ID) {
			delete(s.assets, key)
			counts.Assets++
		}
	}

	if s.db != nil {
		if err := s.removeAssetFromPostgres(tenant, assetID); err != nil {
			s.lastErr = err.Error()
			return counts, err
		}
	}
	return counts, s.persistLocked()
}

func (s *Store) removeAssetFromPostgres(tenant string, assetID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// One transaction, because a half-removed asset is worse than none: the
	// console would show a machine with no events, or events belonging to a
	// machine that no longer exists.
	for _, statement := range []string{
		`DELETE FROM promtact_actions WHERE asset_id = $1`,
		`DELETE FROM promtact_alerts WHERE asset_id = $1`,
		`DELETE FROM promtact_events WHERE asset_id = $1`,
		`DELETE FROM promtact_assets WHERE id = $1`,
	} {
		if _, err := tx.ExecContext(ctx, statement, assetID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FindAsset reports whether an asset exists, so a caller can refuse a removal
// for something that was never there rather than reporting a cheerful zero.
func (s *Store) FindAsset(tenant string, assetID string) (domain.Asset, bool) {
	assetID = strings.TrimSpace(assetID)
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, asset := range s.assets {
		if !strings.EqualFold(strings.TrimSpace(asset.ID), assetID) {
			continue
		}
		if strings.TrimSpace(tenant) != "" && strings.TrimSpace(asset.Tenant) != "" &&
			!strings.EqualFold(strings.TrimSpace(asset.Tenant), strings.TrimSpace(tenant)) {
			continue
		}
		return asset, true
	}
	return domain.Asset{}, false
}
