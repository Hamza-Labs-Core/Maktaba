// Package main is the entry point for the Maktaba streaming server.
//
// Stub created by Story 22.1; Story 08.x replaces it with the real
// streaming server.
package main

import (
	"fmt"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Fprintln(os.Stdout, version.String())
		return
	}
	fmt.Fprintf(os.Stdout, "maktaba-streaming %s: stub (Story 08 will replace this)\n", version.String())
}
