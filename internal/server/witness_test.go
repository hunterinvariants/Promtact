package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeWitness is a stand-in for the Cloudflare Worker with the same refusal
// rules: it will not accept a shorter chain, and will not accept a different
// head for an index it already holds.
type fakeWitness struct {
	mu      sync.Mutex
	byIndex map[int]string
	latest  witnessState
	server  *httptest.Server
}

func newFakeWitness(t *testing.T) *fakeWitness {
	t.Helper()
	f := &fakeWitness{byIndex: map[int]string{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/anchor" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()

		if r.Method == http.MethodGet {
			if f.latest.Head == "" && f.latest.Index == 0 && len(f.byIndex) == 0 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(f.latest)
			return
		}

		var submitted struct {
			Index int    `json:"chain_index"`
			Head  string `json:"head"`
		}
		if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if submitted.Index < f.latest.Index {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("the submitted chain is shorter than the witnessed chain"))
			return
		}
		if known, ok := f.byIndex[submitted.Index]; ok && known != submitted.Head {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("this index was already witnessed with a different head"))
			return
		}
		f.byIndex[submitted.Index] = submitted.Head
		f.latest = witnessState{Index: submitted.Index, Head: submitted.Head, WitnessAt: time.Now().UTC()}
		_ = json.NewEncoder(w).Encode(f.latest)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// forget simulates the operator rewriting local history: the witness keeps what
// it saw, the local side is what changes.
func (f *fakeWitness) witnessed() witnessState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latest
}

func witnessedApp(t *testing.T, endpoint string) *App {
	t.Helper()
	app, err := NewWithOptions(Options{WitnessEndpoint: endpoint, WitnessToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestAnchorIsPublishedAndReadBack(t *testing.T) {
	fake := newFakeWitness(t)
	app := witnessedApp(t, fake.server.URL)
	ctx := context.Background()

	if err := app.PublishAuditAnchor(ctx); err != nil {
		t.Fatalf("publishing the first anchor failed: %v", err)
	}
	if _, err := app.VerifyAgainstWitness(ctx); err != nil {
		t.Fatalf("a freshly published anchor should verify: %v", err)
	}
}

// The case a local anchor cannot catch: history is truncated, and the shortened
// chain re-anchors against itself perfectly well. Only a witness that remembers
// the previous length notices.
func TestTruncatedHistoryIsDetected(t *testing.T) {
	fake := newFakeWitness(t)
	app := witnessedApp(t, fake.server.URL)
	ctx := context.Background()

	// Witness a chain that is longer than anything this instance holds.
	fake.mu.Lock()
	fake.latest = witnessState{Index: 500, Head: "aaaaaaaaaaaaaaaa"}
	fake.byIndex[500] = "aaaaaaaaaaaaaaaa"
	fake.mu.Unlock()

	_, err := app.VerifyAgainstWitness(ctx)
	if !errors.Is(err, ErrChainDiverged) {
		t.Fatalf("a shorter local chain must be reported as divergence, got %v", err)
	}

	// And publishing must be refused rather than quietly overwriting the record.
	if err := app.PublishAuditAnchor(ctx); !errors.Is(err, ErrChainDiverged) {
		t.Fatalf("publishing a shorter chain must be refused, got %v", err)
	}
}

// Records rewritten in place keep the chain length but change the head.
func TestRewrittenHistoryIsDetected(t *testing.T) {
	fake := newFakeWitness(t)
	app := witnessedApp(t, fake.server.URL)
	ctx := context.Background()

	if err := app.PublishAuditAnchor(ctx); err != nil {
		t.Fatal(err)
	}
	seen := fake.witnessed()

	// The operator rewrites the records: same count, different head.
	fake.mu.Lock()
	fake.byIndex[seen.Index] = "a-different-head-entirely"
	fake.latest.Head = "a-different-head-entirely"
	fake.mu.Unlock()

	if _, err := app.VerifyAgainstWitness(ctx); !errors.Is(err, ErrChainDiverged) {
		t.Fatalf("a rewritten head must be reported as divergence, got %v", err)
	}
}

// A divergence must not be cleared by the next successful publish. Otherwise an
// operator rewrites history, waits one interval, and the alarm goes quiet by
// itself.
func TestDivergenceIsStickyUntilVerified(t *testing.T) {
	fake := newFakeWitness(t)
	app := witnessedApp(t, fake.server.URL)

	app.witness.noteMismatch("something disagreed")
	if _, flagged, _ := app.witness.status(); !flagged {
		t.Fatal("the mismatch was not recorded")
	}

	// A publish that succeeds must not silently clear it.
	if err := app.PublishAuditAnchor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, flagged, _ := app.witness.status(); !flagged {
		t.Fatal("a successful publish cleared a recorded divergence")
	}

	// Only an explicit verification that agrees may clear it.
	if _, err := app.VerifyAgainstWitness(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, flagged, _ := app.witness.status(); flagged {
		t.Fatal("verification that agreed did not clear the divergence")
	}
}

// An unreachable witness must not take the service down or block a request. It
// is evidence infrastructure, not a dependency of enforcement.
func TestUnreachableWitnessDoesNotBreakTheService(t *testing.T) {
	app := witnessedApp(t, "http://127.0.0.1:1")

	start := time.Now()
	if err := app.PublishAuditAnchor(context.Background()); err == nil {
		t.Fatal("an unreachable witness should report an error")
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("publishing blocked for %s", elapsed)
	}

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("requests failed while the witness was unreachable: %d", rec.Code)
	}
}

// Without an endpoint nothing is published and nothing is claimed.
func TestWitnessIsOffByDefault(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if app.witness.enabled() {
		t.Fatal("witnessing must be off unless an endpoint is configured")
	}
	if err := app.PublishAuditAnchor(context.Background()); err != nil {
		t.Fatalf("publishing with no witness configured should be a no-op: %v", err)
	}
	if _, err := app.VerifyAgainstWitness(context.Background()); err == nil {
		t.Fatal("verifying without a witness must not report success")
	}
}
