//go:build e2etrust

package releaseverify

import (
	"os"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// e2eTrustedRoot is the trusted root of a fake Sigstore (releaseverifytest) for an
// end-to-end test's builds: the file MAKTABA_E2E_TRUSTED_ROOT names. Only binaries
// built with -tags e2etrust have it; a release build never reads that variable.
func e2eTrustedRoot() (root.TrustedMaterial, bool) {
	p := os.Getenv("MAKTABA_E2E_TRUSTED_ROOT")
	if p == "" {
		return nil, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	tr, err := root.NewTrustedRootFromJSON(b)
	if err != nil {
		return nil, false
	}
	return tr, true
}
