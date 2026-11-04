// catalog-dump prints the embedded component catalog as canonical JSON: `make
// catalog-sync` writes it to shared/components/testdata/catalog.dump.json, the answer
// both loaders (Go and the pipeline's) are tested against.
package main

import (
	"fmt"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

func main() {
	c, err := components.Load(components.Raw())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b, err := c.Dump()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(b)
}
