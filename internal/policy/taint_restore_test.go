package policy

import (
	"errors"
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// A mark that a restart forgets is not a control.
//
// This is the failure that would never have announced itself: a deploy releases
// every marked session at once, nothing errors, nothing logs, and the next
// outward action from a session that read a poisoned page is simply allowed.
// Working and broken produce identical output.

type fakeTaintStore struct {
	rows      map[string]SessionTaintRecord
	saveErr   error
	loadErr   error
	saveCalls int
}

func newFakeTaintStore() *fakeTaintStore {
	return &fakeTaintStore{rows: map[string]SessionTaintRecord{}}
}

func (f *fakeTaintStore) SaveSessionTaint(tenant string, key string, marks []string, at time.Time) error {
	f.saveCalls++
	if f.saveErr != nil {
		return f.saveErr
	}
	f.rows[key] = SessionTaintRecord{Tenant: tenant, Key: key, Marks: append([]string(nil), marks...), TaintedAt: at}
	return nil
}

func (f *fakeTaintStore) LoadSessionTaint(since time.Time) ([]SessionTaintRecord, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	var out []SessionTaintRecord
	for _, row := range f.rows {
		if !row.TaintedAt.Before(since) {
			out = append(out, row)
		}
	}
	return out, nil
}

func TestSessionMarkSurvivesRestart(t *testing.T) {
	backing := newFakeTaintStore()

	// The process that read the page.
	first := New(Config{ApprovedTools: []string{"asset_inventory", "ticket_create"}})
	first.SetTaintStore(backing)
	fetch := sessionCall("s-restart", "asset_inventory", "read a page", "https://status.vendor.example")
	first.RecordToolResultTaint(fetch, first.InspectToolResult(fetch, "ordinary content").Taint)

	// The process after a restart: same store, no memory of anything.
	second := New(Config{ApprovedTools: []string{"asset_inventory", "ticket_create"}})
	second.SetTaintStore(backing)

	before := second.GateToolCall(sessionCall("s-restart", "ticket_create", "post the summary to the internal tracker", ""))
	if before.Verdict != domain.GatewayAllow {
		t.Fatalf("the baseline is already held (%s), so this test cannot show what restoring adds", before.Verdict)
	}

	restored, err := second.RestoreSessionTaint()
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored == 0 {
		t.Fatal("nothing was restored")
	}

	after := second.GateToolCall(sessionCall("s-restart", "ticket_create", "post the summary to the internal tracker", ""))
	if after.Verdict == domain.GatewayAllow {
		t.Error("the mark did not survive the restart: the session was released silently")
	}
}

func TestExpiredMarksAreNotRestored(t *testing.T) {
	backing := newFakeTaintStore()
	backing.rows["stale"] = SessionTaintRecord{
		Key:       "session_id:s-old",
		Marks:     []string{"tool_result:vendor.example"},
		TaintedAt: time.Now().UTC().Add(-48 * time.Hour),
	}

	engine := New(Config{ApprovedTools: []string{"ticket_create"}})
	engine.SetTaintStore(backing)

	restored, err := engine.RestoreSessionTaint()
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	// Loading marks that expired two days ago would hold actions for sessions
	// that ended long before the process started.
	if restored != 0 {
		t.Errorf("restored %d expired mark(s)", restored)
	}
}

func TestStorageFailureStillMarksThisProcess(t *testing.T) {
	backing := newFakeTaintStore()
	backing.saveErr = errors.New("database is down")

	engine := New(Config{ApprovedTools: []string{"asset_inventory", "ticket_create"}})
	engine.SetTaintStore(backing)

	fetch := sessionCall("s-degraded", "asset_inventory", "read a page", "https://status.vendor.example")
	engine.RecordToolResultTaint(fetch, engine.InspectToolResult(fetch, "ordinary content").Taint)

	// Losing the database must not make the running process less safe than it
	// would have been without one at all.
	held := engine.GateToolCall(sessionCall("s-degraded", "ticket_create", "post the summary to the internal tracker", ""))
	if held.Verdict == domain.GatewayAllow {
		t.Error("a storage failure silently disabled the control for this process")
	}
	// And it has to be visible, because the consequence - marks no longer
	// surviving a restart - is invisible until the restart happens.
	if engine.TaintStoreError() == "" {
		t.Error("the storage failure was swallowed; nothing would tell an operator marks are no longer durable")
	}
}

func TestNoStoreConfiguredIsNotAnError(t *testing.T) {
	engine := New(Config{ApprovedTools: []string{"asset_inventory"}})

	// A deployment running in memory has nowhere to put these and should not be
	// made to pretend otherwise.
	restored, err := engine.RestoreSessionTaint()
	if err != nil || restored != 0 {
		t.Errorf("restore without a store returned (%d, %v)", restored, err)
	}
	if engine.TaintStoreError() != "" {
		t.Errorf("an unconfigured store reported an error: %q", engine.TaintStoreError())
	}
}
