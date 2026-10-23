//go:build !darwin

package updater

import "errors"

// cloneTree: no copy-on-write clone here; copyTree copies.
func cloneTree(string, string) error { return errors.ErrUnsupported }
