package server

import (
	_ "embed"
	"net/http"
)

// The specification is embedded so it ships with the binary and cannot drift
// away from the deployment an integrator is actually talking to.
//
//go:embed openapi.json
var openAPISpec []byte

func (a *App) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(openAPISpec)
}
