package server

import (
	"context"
	"encoding/json"
	"log"
	"sync/atomic"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// Degraded operation: the policy verdict is computed in process and never
// depends on storage, so a database outage must not change what the gateway
// decides. Only the durable record is affected, and that is deferred to the
// local journal instead of failing the request.
//
// Enforcement is not weakened while degraded: deny stays deny, and
// require_approval stays require_approval — the caller is still told to wait,
// and because the pending action cannot be recorded it also cannot be approved
// during the outage, which is the safe direction.

var reconcileInFlight atomic.Bool

func (a *App) markDegraded(reason string) {
	a.degradedMu.Lock()
	defer a.degradedMu.Unlock()
	if a.degradedSince.IsZero() {
		a.degradedSince = time.Now().UTC()
		log.Printf("gateway entering degraded mode: %s", sanitizeLogValue(reason))
	}
	a.degradedReason = reason
}

func (a *App) clearDegraded() {
	a.degradedMu.Lock()
	wasDegraded := !a.degradedSince.IsZero()
	a.degradedSince = time.Time{}
	a.degradedReason = ""
	a.degradedMu.Unlock()
	if wasDegraded {
		log.Print("gateway left degraded mode: persistence recovered")
	}
}

// DegradedState reports whether durable persistence is currently failing.
func (a *App) DegradedState() (bool, time.Time, string) {
	a.degradedMu.Lock()
	defer a.degradedMu.Unlock()
	return !a.degradedSince.IsZero(), a.degradedSince, a.degradedReason
}

// persistAlerts stores alerts, or journals them when storage is unavailable.
// It returns the alerts to report on the decision either way, so a storage
// outage never removes an alert from the answer the caller receives.
func (a *App) persistAlerts(tenant string, alerts []domain.Alert) []domain.Alert {
	if len(alerts) == 0 {
		return nil
	}
	added, err := a.addAlertsForTenant(alerts, tenant)
	if err == nil {
		a.onPersistSuccess()
		return added
	}
	a.markDegraded(err.Error())
	if journalErr := a.journal.Append(journalKindAlerts, tenant, err.Error(), alerts); journalErr != nil {
		log.Printf("could not journal alerts during degraded mode: %s", sanitizeLogValue(journalErr.Error()))
	}
	return alerts
}

// persistActions stores gateway actions, or journals them when storage is
// unavailable, so a denial or approval requirement is still recorded.
func (a *App) persistActions(tenant string, actions []domain.ResponseAction) {
	if len(actions) == 0 {
		return
	}
	if err := a.addActionsForTenant(actions, tenant); err == nil {
		a.onPersistSuccess()
		return
	} else {
		a.markDegraded(err.Error())
		if journalErr := a.journal.Append(journalKindActions, tenant, err.Error(), actions); journalErr != nil {
			log.Printf("could not journal actions during degraded mode: %s", sanitizeLogValue(journalErr.Error()))
		}
	}
}

// onPersistSuccess marks storage healthy again and reconciles any backlog. The
// drain runs in the background so a recovering deployment does not pay the
// backlog cost inside a request.
func (a *App) onPersistSuccess() {
	a.clearDegraded()
	if !a.journal.enabled() || a.journal.Depth() == 0 {
		return
	}
	if !reconcileInFlight.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer reconcileInFlight.Store(false)
		if applied, err := a.ReconcileJournal(); err != nil {
			log.Printf("journal reconciliation stopped after %d entries: %s", applied, sanitizeLogValue(err.Error()))
		} else if applied > 0 {
			log.Printf("journal reconciliation replayed %d entries", applied)
		}
	}()
}

// ReconcileJournal replays journalled records into storage. It is safe to call
// repeatedly; entries that still cannot be stored stay in the journal.
func (a *App) ReconcileJournal() (int, error) {
	return a.journal.Drain(func(entry journalEntry) error {
		switch entry.Kind {
		case journalKindAlerts:
			var alerts []domain.Alert
			if err := json.Unmarshal(entry.Payload, &alerts); err != nil {
				return err
			}
			_, err := a.addAlertsForTenant(alerts, entry.Tenant)
			return err
		case journalKindActions:
			var actions []domain.ResponseAction
			if err := json.Unmarshal(entry.Payload, &actions); err != nil {
				return err
			}
			return a.addActionsForTenant(actions, entry.Tenant)
		default:
			return nil
		}
	})
}

// StartJournalReconciler drains the backlog periodically, so a deployment that
// recovers while idle does not keep records on local disk indefinitely.
func (a *App) StartJournalReconciler(ctx context.Context, every time.Duration) {
	if !a.journal.enabled() {
		return
	}
	if every <= 0 {
		every = time.Minute
	}
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if a.journal.Depth() == 0 {
					continue
				}
				if !reconcileInFlight.CompareAndSwap(false, true) {
					continue
				}
				applied, err := a.ReconcileJournal()
				reconcileInFlight.Store(false)
				if err == nil && applied > 0 {
					a.clearDegraded()
				}
			}
		}
	}()
}
