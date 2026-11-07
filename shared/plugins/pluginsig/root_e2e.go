//go:build e2etrust

package pluginsig

import "os"

// e2eRoot is a test build's plugin root: the file MAKTABA_E2E_PLUGINS_ROOT names (test keys
// made by the store's signing tools). Only binaries built with -tags e2etrust have it.
func e2eRoot() ([]byte, bool) { return e2eFile("MAKTABA_E2E_PLUGINS_ROOT") }

func e2eFile(env string) ([]byte, bool) {
	p := os.Getenv(env)
	if p == "" {
		return nil, false
	}
	b, err := os.ReadFile(p)
	return b, err == nil
}
