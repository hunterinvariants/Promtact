package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
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
//
// The signature inherits the policy file's ownership and permissions rather
// than being written 0600 root-only.
//
// Signing is normally done as root, while the service runs as its own user. A
// root-only signature is therefore unreadable to the process that has to verify
// it, and because an unreadable file and an absent one are the same branch in
// verifyPolicySignature, the service refuses to start - with a message saying
// the signature is missing when it is sitting right there. On a host that
// restarts daily that turns one forgotten chmod into a service that does not
// come back, hours later, for a reason the message misdescribes.
//
// The signature is not secret: it is an HMAC that proves the policy was not
// altered, and it is worthless to anyone without the key. Matching the policy's
// own mode is both sufficient and correct.
func SignPolicyFile(path string) (string, error) {
	key := policyHMACKey()
	if len(key) == 0 {
		return "", errors.New("PROMTACT_POLICY_HMAC_SECRET is required to sign a policy")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	// Fall back to 0600 only when the policy's own mode cannot be read, which
	// should not happen given the ReadFile above succeeded.
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	sigPath := path + ".sig"
	if err := os.WriteFile(sigPath, []byte(PolicySignature(data, key)+"\n"), mode); err != nil {
		return "", err
	}
	// WriteFile does not apply the mode to a file that already exists, and a
	// re-signed policy is the common case rather than the rare one.
	if err := os.Chmod(sigPath, mode); err != nil {
		return "", err
	}
	if err := matchOwner(path, sigPath); err != nil {
		return "", err
	}
	return sigPath, nil
}

// CheckPolicyFile answers the question the service will ask at its next start,
// now, while somebody is still watching.
//
// The failure this exists for is entirely a timing problem: editing a policy and
// forgetting to re-sign it changes nothing until the next restart, which on a
// host that reboots daily happens hours later, unattended, and takes the service
// down until a human intervenes. The check costs nothing and can run in a shell,
// in a deploy script, or as an ExecStartPre.
//
// It reads the same files, with the same key, through the same comparison as
// startup - deliberately not a reimplementation, because a check that can
// disagree with the thing it is checking is worse than no check.
func CheckPolicyFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return verifyPolicySignature(path, data)
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
			// "Missing" and "there but unreadable" are the same branch and very
			// different problems, and the second is the likely one: signing runs
			// as root, the service runs as its own user. Reporting a file that
			// is sitting right there as missing sends the reader to look for the
			// wrong fault, at a restart, with the service down.
			if errors.Is(sigErr, fs.ErrPermission) {
				return fmt.Errorf("policy signature %q exists but this process cannot read it: %w"+
					" (signing usually runs as root while the service runs as its own user;"+
					" the signature needs the same owner and mode as the policy)", sigPath, sigErr)
			}
			if errors.Is(sigErr, fs.ErrNotExist) {
				return fmt.Errorf("policy signature %q is missing but a signing key is configured:"+
					" run `promtactl sign-policy --file %s` after every change to the policy", sigPath, path)
			}
			return fmt.Errorf("policy signature %q could not be read: %w", sigPath, sigErr)
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
