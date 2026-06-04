// Command maktaba-server is the unified Maktaba entry point: one binary
// that supervises the API, streaming, and pipeline services, serves the
// embedded web UI, and carries every CLI subcommand an installer needs
// (setup, update, uninstall, migrate, adduser, keys, models).
//
// It deliberately owns lifecycle + configuration only — the heavy
// service logic lives in the existing api/streaming/pipeline codebases,
// which this binary launches as managed child processes driven by a
// single server.toml (see internal/supervisor + internal/config).
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra already prints the error; exit non-zero for scripts.
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "maktaba-server",
		Short:         "Maktaba unified server + installer CLI",
		Long:          rootLong,
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       version.String(),
	}
	// Print our richer version line for `--version`.
	root.SetVersionTemplate("{{.Version}}\n")

	root.AddCommand(
		newServeCmd(),
		newSetupCmd(),
		newUpdateCmd(),
		newUninstallCmd(),
		newModelsCmd(),
		newMigrateCmd(),
		newAddUserCmd(),
		newKeysCmd(),
	)
	return root
}

const rootLong = `maktaba-server runs all of Maktaba from a single binary.

  maktaba-server setup     First-run wizard: write config, run migrations
  maktaba-server serve     Start the API, streaming, pipeline, and web UI
  maktaba-server update    Self-update to the latest release
  maktaba-server uninstall Guided teardown
  maktaba-server models    Manage Whisper transcription models
  maktaba-server migrate   Run database migrations
  maktaba-server adduser   Seed the first admin user
  maktaba-server keys      Generate or rotate JWT signing keys

Configuration lives in server.toml (see 'maktaba-server setup').`

// fatalf prints to stderr and exits 1 — used by leaf commands that want
// a bare error line without cobra usage noise.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
