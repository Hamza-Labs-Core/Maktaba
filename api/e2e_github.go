//go:build e2etrust

package main

import (
	"os"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/schemaguard"
)

// e2eGitHubAPI is the fake GitHub an end-to-end test's build reads releases from
// (MAKTABA_E2E_GITHUB_API; autoupdate_e2e_test.go). Only builds with -tags e2etrust have
// it: a release build always reads api.github.com.
func e2eGitHubAPI() string { return os.Getenv("MAKTABA_E2E_GITHUB_API") }

// e2eSchemaRev, set at link time in an end-to-end test's build (-X
// main.e2eSchemaRev=150), is the newest migration that build's code takes for its own:
// the test's next release ships a migration past this tree's schema revision.
var e2eSchemaRev string

// knownSchemaRev is the newest migration this code knows (serve's guard).
func knownSchemaRev() int64 {
	if n, err := strconv.ParseInt(e2eSchemaRev, 10, 64); err == nil && n > 0 {
		return n
	}
	return schemaguard.SchemaRev
}
