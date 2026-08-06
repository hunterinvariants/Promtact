package server

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/store"
)

// storeDirectory adapts the Postgres-backed tenant directory to the auth
// package's Directory interface. The adapter lives here so neither auth nor
// store has to depend on the other.
//
// It also carries a small cache that is used *only* when the directory cannot be
// reached. In normal operation every request is resolved against the database,
// so revoking a key or suspending a tenant takes effect immediately — the cache
// never delays that. It comes into play during an outage, where it keeps already
// known agents working instead of failing every call.
//
// The exposure this adds is narrow: revoking a key writes to the same database,
// so during an outage nothing can be revoked anyway. The only widened window is
// a key revoked shortly before the outage began, bounded by the configured TTL.
type storeDirectory struct {
	ttl time.Duration

	// Injectable so the cache semantics can be tested without a database.
	lookupByToken       func(context.Context, string) (store.Identity, bool, error)
	lookupByCredentials func(context.Context, string, string) (store.Identity, bool, error)

	mu      sync.RWMutex
	entries map[string]cachedIdentity
}

type cachedIdentity struct {
	identity auth.Identity
	cachedAt time.Time
}

func newStoreDirectory(st *store.Store, ttl time.Duration) *storeDirectory {
	directory := &storeDirectory{ttl: ttl, entries: make(map[string]cachedIdentity)}
	if st != nil {
		directory.lookupByToken = st.IdentityByTokenHash
		directory.lookupByCredentials = st.IdentityByCredentials
	}
	return directory
}

func (d *storeDirectory) remember(key string, identity auth.Identity) {
	if d.ttl <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries[key] = cachedIdentity{identity: identity, cachedAt: time.Now()}
}

// recall returns a cached identity if one is still within the TTL. It is only
// consulted after the directory itself has failed.
func (d *storeDirectory) recall(key string) (auth.Identity, bool) {
	if d.ttl <= 0 {
		return auth.Identity{}, false
	}
	d.mu.RLock()
	entry, ok := d.entries[key]
	d.mu.RUnlock()
	if !ok || time.Since(entry.cachedAt) > d.ttl {
		return auth.Identity{}, false
	}
	return entry.identity, true
}

func (d *storeDirectory) IdentityByTokenHash(ctx context.Context, tokenHash string) (auth.Identity, bool) {
	if d.lookupByToken == nil {
		return auth.Identity{}, false
	}
	identity, ok, err := d.lookupByToken(ctx, tokenHash)
	if err == nil {
		if !ok {
			return auth.Identity{}, false
		}
		resolved := auth.Identity{Name: identity.Name, Tenant: identity.Tenant, Roles: identity.Roles}
		d.remember(tokenHash, resolved)
		return resolved, true
	}

	if cached, found := d.recall(tokenHash); found {
		log.Printf("directory unavailable, authenticating %q from cache", sanitizeLogValue(cached.Name))
		return cached, true
	}
	return auth.Identity{}, false
}

func (d *storeDirectory) IdentityByCredentials(ctx context.Context, username string, tokenHash string) (auth.Identity, bool) {
	if d.lookupByCredentials == nil {
		return auth.Identity{}, false
	}
	identity, ok, err := d.lookupByCredentials(ctx, username, tokenHash)
	if err == nil {
		if !ok {
			return auth.Identity{}, false
		}
		resolved := auth.Identity{Name: identity.Name, Tenant: identity.Tenant, Roles: identity.Roles}
		d.remember(username+"|"+tokenHash, resolved)
		return resolved, true
	}

	if cached, found := d.recall(username + "|" + tokenHash); found {
		log.Printf("directory unavailable, authenticating %q from cache", sanitizeLogValue(cached.Name))
		return cached, true
	}
	return auth.Identity{}, false
}
