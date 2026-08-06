package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const samplePolicy = `{"approved_tools":["asset_inventory"],"users":[{"name":"admin","token_sha256":"abc","roles":["admin"]}]}`

func TestSignedPolicyLoads(t *testing.T) {
	t.Setenv("PROMTACT_POLICY_HMAC_SECRET", "policy-key")
	path := writePolicy(t, samplePolicy)

	if _, err := SignPolicyFile(path); err != nil {
		t.Fatalf("sign: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("a correctly signed policy must load: %v", err)
	}
	if len(cfg.ApprovedTools) != 1 || len(cfg.Users) != 1 {
		t.Fatalf("policy did not parse: %+v", cfg)
	}
}

// The point of signing: an altered policy must not take effect. Tampering here
// would otherwise let an attacker with file access grant themselves admin or
// approve a tool.
func TestTamperedPolicyIsRejected(t *testing.T) {
	t.Setenv("PROMTACT_POLICY_HMAC_SECRET", "policy-key")
	path := writePolicy(t, samplePolicy)
	if _, err := SignPolicyFile(path); err != nil {
		t.Fatal(err)
	}

	tampered := `{"approved_tools":["asset_inventory"],"users":[{"name":"attacker","token_sha256":"deadbeef","roles":["admin"]}]}`
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a tampered policy must be rejected")
	}
}

// A configured key means signatures are enforced: removing the signature must
// not be a way to bypass verification.
func TestMissingSignatureFailsClosed(t *testing.T) {
	t.Setenv("PROMTACT_POLICY_HMAC_SECRET", "policy-key")
	path := writePolicy(t, samplePolicy)

	if _, err := Load(path); err == nil {
		t.Fatal("a missing signature must fail closed while a key is configured")
	}
}

func TestSignatureFromAnotherKeyIsRejected(t *testing.T) {
	path := writePolicy(t, samplePolicy)
	t.Setenv("PROMTACT_POLICY_HMAC_SECRET", "attacker-key")
	if _, err := SignPolicyFile(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROMTACT_POLICY_HMAC_SECRET", "real-key")
	if _, err := Load(path); err == nil {
		t.Fatal("a signature made with a different key must be rejected")
	}
}

// Without a key the behavior is unchanged, so existing deployments keep working.
func TestUnsignedPolicyLoadsWithoutKey(t *testing.T) {
	t.Setenv("PROMTACT_POLICY_HMAC_SECRET", "")
	path := writePolicy(t, samplePolicy)
	if _, err := Load(path); err != nil {
		t.Fatalf("without a key an unsigned policy must still load: %v", err)
	}
}

// An operator can demand signing even before a key is distributed, so a
// misconfigured host refuses to start rather than running unverified.
func TestRequireSignedWithoutKeyRefuses(t *testing.T) {
	t.Setenv("PROMTACT_POLICY_HMAC_SECRET", "")
	t.Setenv("PROMTACT_POLICY_REQUIRE_SIGNED", "true")
	path := writePolicy(t, samplePolicy)
	if _, err := Load(path); err == nil {
		t.Fatal("requiring signatures without a key must refuse to load")
	}
}

func TestSignPolicyFileNeedsKey(t *testing.T) {
	t.Setenv("PROMTACT_POLICY_HMAC_SECRET", "")
	path := writePolicy(t, samplePolicy)
	if _, err := SignPolicyFile(path); err == nil {
		t.Fatal("signing without a key must fail")
	}
}
