//go:build windows

package main

import "errors"

// reexec: Windows runs no program in place of another; the launcher put in place takes
// over at the next start of its task or service (it speaks this one's protocol until
// then: a release that cannot run under it is refused before it installs).
func reexec(string) error {
	return errors.New("the new launcher takes over at the next start (Windows)")
}
