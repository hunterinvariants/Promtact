//go:build !unix

package config

// matchOwner is a no-op where there is no POSIX ownership to copy. The
// permission problem it solves - a root-written signature the service user
// cannot read - does not arise on these platforms.
func matchOwner(source string, target string) error { return nil }
