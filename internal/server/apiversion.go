package server

import (
	"net/http"
	"strings"
)

// The API is versioned by mirroring: every endpoint is reachable both at its
// original path and under /api/v1. Nothing breaks — the validation suite, the
// LangChain connector, the console and deployed agents all speak the original
// paths — while new integrations can pin a version that will not move under them.
//
// The rewrite happens before authentication on purpose. Authorization matches on
// concrete prefixes such as /api/admin/, so a versioned path that reached the
// auth layer unrewritten would miss those rules and fall through to the generic
// read rule, handing customer data to any viewer. Canonicalizing first means
// every existing rule keeps applying unchanged.

const (
	apiVersion       = "v1"
	versionedPrefix  = "/api/" + apiVersion + "/"
	apiVersionHeader = "X-API-Version"
)

// canonicalAPIPath maps a versioned request path onto the internal one.
func canonicalAPIPath(path string) (string, bool) {
	if !strings.HasPrefix(path, versionedPrefix) {
		return path, false
	}
	return "/api/" + strings.TrimPrefix(path, versionedPrefix), true
}

// withAPIVersion rewrites /api/v1/... to its canonical path, and marks
// unversioned API requests as deprecated so integrators can migrate before the
// original paths are ever retired.
func withAPIVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if canonical, versioned := canonicalAPIPath(r.URL.Path); versioned {
			w.Header().Set(apiVersionHeader, apiVersion)
			rewritten := r.Clone(r.Context())
			rewritten.URL.Path = canonical
			next.ServeHTTP(w, rewritten)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			// RFC 8594: announce the successor without changing behaviour.
			w.Header().Set("Deprecation", "true")
			w.Header().Set("Link", "</api/"+apiVersion+strings.TrimPrefix(r.URL.Path, "/api")+">; rel=\"successor-version\"")
		}
		next.ServeHTTP(w, r)
	})
}
