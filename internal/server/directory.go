package server

import (
	"context"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/store"
)

// storeDirectory adapts the Postgres-backed tenant directory to the auth
// package's Directory interface. The adapter lives here so neither auth nor
// store has to depend on the other.
type storeDirectory struct {
	store *store.Store
}

func (d storeDirectory) IdentityByTokenHash(ctx context.Context, tokenHash string) (auth.Identity, bool) {
	identity, ok := d.store.IdentityByTokenHash(ctx, tokenHash)
	if !ok {
		return auth.Identity{}, false
	}
	return auth.Identity{Name: identity.Name, Tenant: identity.Tenant, Roles: identity.Roles}, true
}

func (d storeDirectory) IdentityByCredentials(ctx context.Context, username string, tokenHash string) (auth.Identity, bool) {
	identity, ok := d.store.IdentityByCredentials(ctx, username, tokenHash)
	if !ok {
		return auth.Identity{}, false
	}
	return auth.Identity{Name: identity.Name, Tenant: identity.Tenant, Roles: identity.Roles}, true
}
