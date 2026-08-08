//go:build unix

package config

import (
	"fmt"
	"os"
	"syscall"
)

// matchOwner gives the signature the same owner and group as the file it
// signs.
//
// Policies are conventionally installed root-owned with a group the service
// belongs to, and the signature has to be readable by exactly the same process.
// Copying the ownership is what makes "signed" and "startable" the same
// condition rather than two that have to be remembered separately.
func matchOwner(source string, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if err := os.Chown(target, int(stat.Uid), int(stat.Gid)); err != nil {
		// Not fatal on its own: an unprivileged signer cannot chown, and if the
		// resulting file is readable anyway there is nothing wrong. Saying so is
		// still better than leaving the operator to discover it at the next
		// restart.
		return fmt.Errorf("the signature was written but could not be given the policy's owner (%w);"+
			" check that the service user can read %s", err, target)
	}
	return nil
}
