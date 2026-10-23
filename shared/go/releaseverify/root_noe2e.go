//go:build !e2etrust

package releaseverify

import "github.com/sigstore/sigstore-go/pkg/root"

// e2eTrustedRoot: a release build trusts only the Sigstore roots (root_e2e.go is a
// test build's).
func e2eTrustedRoot() (root.TrustedMaterial, bool) { return nil, false }
