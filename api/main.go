// Package main is the entry point for the Maktaba API server.
//
// Story 22.1 created the binary as a stub; Story 22.2 wired the
// `--version` flag and reproducibility envelope; Story 22.4 added the
// `migrate` subcommand. Story 07.x replaces the serve path with the
// real HTTP server.
package main

import (
	"fmt"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--version":
			fmt.Fprintln(os.Stdout, version.String())
			return
		case "migrate":
			if err := runMigrate(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
				os.Exit(1)
			}
			return
		case "help", "-h", "--help":
			printUsage(os.Stdout)
			return
		}
	}
	fmt.Fprintf(os.Stdout, "maktaba-api %s: stub (Story 07 will replace this)\n", version.String())
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, `maktaba-api — Maktaba server binary

Usage:
  maktaba-api <command> [flags]

Commands:
  migrate    Run database migrations (see "migrate --help")
  --version  Print the binary's version
  help       Show this help`)
}
