package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
)

// The policy file decides which tools are approved, which principals exist, what
// roles they hold, which tool fingerprints are pinned and which agent identities
// are registered. It is read from local disk at startup — including a restart
// during a database outage, when it is the only source of that truth.
//
// Signing it makes tampering detectable: an attacker with file access can no
// longer quietly add an admin user or approve a tool. The key is separate from
// the threat-pack key on purpose, so a compromised detection-content key cannot
// also forge identities.

// PolicySignature returns the HMAC-SHA256 of the policy document.
func PolicySignature(data []byte, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func policyHMACKey() []byte {
	secret := strings.TrimSpace(os.Getenv("PROMTACT_POLICY_HMAC_SECRET"))
	if secret == "" {
		return nil
	}
	return []byte(secret)
}

func policySignatureRequired() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PROMTACT_POLICY_REQUIRE_SIGNED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// SignPolicyFile writes the detached signature for a policy document.
func SignPolicyFile(path string) (string, error) {
	key := policyHMACKey()
	if len(key) == 0 {
		return "", errors.New("PROMTACT_POLICY_HMAC_SECRET is required to sign a policy")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sigPath := path + ".sig"
	if err := os.WriteFile(sigPath, []byte(PolicySignature(data, key)+"\n"), 0o600); err != nil {
		return "", err
	}
	return sigPath, nil
}

// verifyPolicySignature enforces the detached signature next to the policy file.
// When a key is configured this fails closed: a missing or mismatched signature
// stops startup rather than loading a document that may have been altered.
func verifyPolicySignature(path string, data []byte) error {
	key := policyHMACKey()
	required := policySignatureRequired()
	sigPath := path + ".sig"
	sigBytes, sigErr := os.ReadFile(sigPath)

	if len(key) > 0 {
		if sigErr != nil {
			return fmt.Errorf("policy signature %q is missing but a signing key is configured: %w", sigPath, sigErr)
		}
		expected := PolicySignature(data, key)
		provided := strings.TrimSpace(string(sigBytes))
		if !hmac.Equal([]byte(expected), []byte(provided)) {
			return fmt.Errorf("policy signature mismatch for %q", path)
		}
		return nil
	}

	if required {
		return errors.New("signed policy required but PROMTACT_POLICY_HMAC_SECRET is not set")
	}
	if sigErr == nil {
		log.Printf("warning: policy %q has a signature but no key is configured to verify it", sanitizePathForLog(path))
	}
	return nil
}

// sanitizePathForLog keeps a path from breaking or forging log lines.
func sanitizePathForLog(path string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || (r < 0x20 && r != '\t') {
			return ' '
		}
		return r
	}, path)
}
