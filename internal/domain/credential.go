package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// Brokered credentials: the gateway holds the key, the agent does not.
//
// Until now an agent held the API key for the tool it called, and Promtact sat
// beside that relationship rather than inside it. An agent that found another
// route - a shell, a direct HTTP call, a file on the same disk - arrived at the
// tool holding a working credential, and the gateway was simply not involved.
// Being in the path was a property of the deployment, not of this software.
//
// Inverting it removes the choice. The tool credential lives here; the agent
// holds only a token this gateway accepts and nothing else does. A route around
// the gateway now arrives without authority, so bypassing stops being a
// shortcut and becomes a dead end - with no cooperation from the agent and no
// network controls required.
//
// What this does not do is worth stating next to what it does: it protects
// tools that authenticate. An unauthenticated internal service, or a file on
// the agent's own disk, is reachable regardless, and only egress control
// reaches that case.

// Credential is a secret the gateway presents upstream on an agent's behalf.
//
// The secret itself is never part of the JSON form. That is not decoration: the
// console, the audit record, the API listing and the log lines all serialise
// these, and a `json:"-"` is the one place the rule can be enforced for all of
// them at once rather than remembered at each call site.
type Credential struct {
	ID     string `json:"id"`
	Tenant string `json:"tenant"`

	// Tool selects which calls this credential is presented for: an exact tool
	// name, a "prefix_*" wildcard, or "*" as the tenant's fallback.
	Tool string `json:"tool"`

	// Header is where the secret goes upstream, default "Authorization".
	// Scheme is the prefix inside that header, default "Bearer".
	Header string `json:"header"`
	Scheme string `json:"scheme"`

	// Secret never leaves this process in serialised form.
	Secret string `json:"-"`

	// Fingerprint lets an operator confirm which secret is installed, and that
	// a rotation actually replaced it, without anyone reading the value back.
	// There is deliberately no API that returns Secret.
	Fingerprint string `json:"fingerprint"`

	Description string     `json:"description,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	RotatedAt   *time.Time `json:"rotated_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	UseCount    int64      `json:"use_count"`
}

// CredentialFingerprint is a short digest used to identify a secret without
// disclosing it. It is truncated because it exists to be compared by eye, and a
// full digest of a low-entropy secret is closer to the secret than it looks.
func CredentialFingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])[:12]
}

// HeaderName is the header this credential is presented in.
func (c Credential) HeaderName() string {
	if name := strings.TrimSpace(c.Header); name != "" {
		return name
	}
	return "Authorization"
}

// HeaderValue is the full value written into that header.
func (c Credential) HeaderValue() string {
	scheme := strings.TrimSpace(c.Scheme)
	if scheme == "" && strings.EqualFold(c.HeaderName(), "Authorization") {
		scheme = "Bearer"
	}
	if scheme == "" {
		return c.Secret
	}
	return scheme + " " + c.Secret
}

// specificity ranks a pattern so the most precise match wins: an exact tool
// name beats a prefix, and a prefix beats the tenant fallback. Without an
// explicit ranking the answer would depend on row order, which is a fine way to
// present the wrong credential to the wrong tool after an unrelated change.
func credentialSpecificity(pattern string) int {
	switch {
	case pattern == "*":
		return 0
	case strings.HasSuffix(pattern, "*"):
		return 1 + len(strings.TrimSuffix(pattern, "*"))
	default:
		return 1000 + len(pattern)
	}
}

func credentialMatches(pattern string, tool string) bool {
	pattern = strings.TrimSpace(strings.ToLower(pattern))
	tool = strings.TrimSpace(strings.ToLower(tool))
	switch {
	case pattern == "" || tool == "":
		return false
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(tool, strings.TrimSuffix(pattern, "*"))
	default:
		return pattern == tool
	}
}

// SelectCredential returns the credential to present for a tool call, or false
// if the tenant has none that applies.
//
// Returning false is not an error and must not be treated as one: a deployment
// that has not adopted brokering yet has no credentials at all, and those calls
// still need to work. The caller falls back to the statically configured
// upstream token in that case.
func SelectCredential(credentials []Credential, tenant string, tool string) (Credential, bool) {
	tenant = strings.TrimSpace(strings.ToLower(tenant))
	if tenant == "" {
		tenant = "default"
	}

	var candidates []Credential
	for _, credential := range credentials {
		owner := strings.TrimSpace(strings.ToLower(credential.Tenant))
		if owner == "" {
			owner = "default"
		}
		if owner != tenant {
			continue
		}
		if credentialMatches(credential.Tool, tool) {
			candidates = append(candidates, credential)
		}
	}
	if len(candidates) == 0 {
		return Credential{}, false
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		si, sj := credentialSpecificity(candidates[i].Tool), credentialSpecificity(candidates[j].Tool)
		if si != sj {
			return si > sj
		}
		// A deterministic tiebreak, so two equally specific patterns do not
		// swap places between restarts.
		return candidates[i].ID < candidates[j].ID
	})
	return candidates[0], true
}
