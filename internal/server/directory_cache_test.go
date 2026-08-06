package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/store"
)

// fakeLookup stands in for the tenant directory so the test can switch between
// "resolved", "no such identity" and "database unreachable".
type fakeLookup struct {
	identity store.Identity
	found    bool
	err      error
	calls    int
}

func (f *fakeLookup) lookup(context.Context, string) (store.Identity, bool, error) {
	f.calls++
	return f.identity, f.found, f.err
}

// directoryWithLookup builds a storeDirectory whose backend is the fake.
func directoryWithLookup(f *fakeLookup, ttl time.Duration) *storeDirectory {
	d := newStoreDirectory(nil, ttl)
	d.lookupByToken = f.lookup
	return d
}

// The cache must never shorten revocation while the directory is healthy: every
// request is resolved against it, so a revoked key stops working immediately.
func TestIdentityCacheNotUsedWhileDirectoryHealthy(t *testing.T) {
	backend := &fakeLookup{identity: store.Identity{Name: "bot", Tenant: "acme", Roles: []string{"operator"}}, found: true}
	directory := directoryWithLookup(backend, time.Minute)

	if _, ok := directory.IdentityByTokenHash(context.Background(), "hash"); !ok {
		t.Fatal("a valid key should authenticate")
	}
	// The key is revoked: the directory now reports it as unknown, with no error.
	backend.identity, backend.found = store.Identity{}, false

	if _, ok := directory.IdentityByTokenHash(context.Background(), "hash"); ok {
		t.Fatal("a revoked key must stop working at once while the directory is healthy")
	}
	if backend.calls != 2 {
		t.Fatalf("every request must consult the directory, got %d calls", backend.calls)
	}
}

// During an outage a previously seen agent keeps working, so a database
// incident does not take every customer's agents offline.
func TestIdentityCacheServesDuringOutage(t *testing.T) {
	backend := &fakeLookup{identity: store.Identity{Name: "bot", Tenant: "acme", Roles: []string{"operator"}}, found: true}
	directory := directoryWithLookup(backend, time.Minute)

	if _, ok := directory.IdentityByTokenHash(context.Background(), "hash"); !ok {
		t.Fatal("expected the first lookup to succeed")
	}

	backend.err = errors.New("database unreachable")
	identity, ok := directory.IdentityByTokenHash(context.Background(), "hash")
	if !ok {
		t.Fatal("a known agent must keep authenticating during an outage")
	}
	if identity.Name != "bot" || identity.Tenant != "acme" {
		t.Fatalf("cached identity is wrong: %+v", identity)
	}
}

// An agent that was never seen cannot be admitted during an outage: failing
// closed is the only safe answer when the directory cannot be consulted.
func TestUnknownIdentityIsRejectedDuringOutage(t *testing.T) {
	backend := &fakeLookup{err: errors.New("database unreachable")}
	directory := directoryWithLookup(backend, time.Minute)

	if _, ok := directory.IdentityByTokenHash(context.Background(), "never-seen"); ok {
		t.Fatal("an unknown identity must not be admitted during an outage")
	}
}

// The cached grant is bounded: once the TTL passes it is no longer honored.
func TestIdentityCacheExpires(t *testing.T) {
	backend := &fakeLookup{identity: store.Identity{Name: "bot", Tenant: "acme"}, found: true}
	directory := directoryWithLookup(backend, 20*time.Millisecond)

	if _, ok := directory.IdentityByTokenHash(context.Background(), "hash"); !ok {
		t.Fatal("expected the first lookup to succeed")
	}
	backend.err = errors.New("database unreachable")
	time.Sleep(40 * time.Millisecond)

	if _, ok := directory.IdentityByTokenHash(context.Background(), "hash"); ok {
		t.Fatal("an expired cache entry must not authenticate")
	}
}

// With the cache disabled the outage fallback does not exist at all.
func TestIdentityCacheCanBeDisabled(t *testing.T) {
	backend := &fakeLookup{identity: store.Identity{Name: "bot", Tenant: "acme"}, found: true}
	directory := directoryWithLookup(backend, 0)

	if _, ok := directory.IdentityByTokenHash(context.Background(), "hash"); !ok {
		t.Fatal("expected the first lookup to succeed")
	}
	backend.err = errors.New("database unreachable")
	if _, ok := directory.IdentityByTokenHash(context.Background(), "hash"); ok {
		t.Fatal("a disabled cache must not authenticate during an outage")
	}
}

var _ auth.Directory = (*storeDirectory)(nil)
