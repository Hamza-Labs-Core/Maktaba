// Package main is the entry point for the Maktaba API server.
//
// This is a stub created by Story 22.1 to give the CI pipeline a real
// Go module to compile. Story 07.x (api-server) replaces it with the
// real server.
package main

import (
	"fmt"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Fprintln(os.Stdout, version.String())
		return
	}
	fmt.Fprintf(os.Stdout, "maktaba-api %s: stub (Story 07 will replace this)\n", version.String())
}
