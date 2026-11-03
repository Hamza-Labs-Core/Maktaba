package components

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner/loopcheck"
)

// startFiles are where the API starts its background loops (story 51.05 §2): a bare
// safego.Go there that is not a catalog loop must say `// not a loop: <why>`.
var startFiles = []string{"main.go", "cluster_bootstrap.go", "node_bootstrap.go", "node_link.go",
	"internal/router/*.go"}

// T08 (F1, Go loops): every loop the API declares is in the catalog, every catalog loop
// of the API is started somewhere, and each loop of a switchable component goes through
// the runner (a safego.Go cannot be stopped).
func TestAPILoopsMatchTheCatalog(t *testing.T) {
	loopcheck.Check(t, loopcheck.Spec{
		Host: Host, Root: "../..", StartFiles: startFiles,
		Declare: []string{"Declare"}, Plain: []loopcheck.Plain{{Pkg: "safego", Func: "Go", NameArg: 1}},
	})
}
