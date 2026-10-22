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

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
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
		// Epic 36 story 36.2 / Epic 38 story 38.1: an install made before this
		// platform's paths (~/Library/Application Support/Maktaba on a Mac,
		// %LOCALAPPDATA%\Maktaba on Windows) comes into them before any command
		// reads it: a Mac install moves, a Windows one's config, keys and node.json
		// are copied (the originals stay for whatever still reads them). Never inside
		// the desktop app's payload (config.InAppPayload): an older install comes
		// into the app only through its move, which keeps the old one for going back.
		PersistentPreRunE: func(*cobra.Command, []string) error {
			exe, _ := os.Executable()
			lines, err := config.MigrateDirs(exe)
			for _, l := range lines {
				fmt.Fprintln(os.Stderr, l)
			}
			if err != nil {
				return fmt.Errorf("bring this install into %s: %w", config.LocalDirs().Config, err)
			}
			return nil
		},
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
		newMigrateFromDevCmd(),
		newServiceCmd(),
		newRuntimeCmd(),
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
  maktaba-server migrate-from-dev  Move the developer stack into Maktaba.app
  maktaba-server service   The systemd user service of the Linux app
  maktaba-server runtime   The Linux app's runtime copy of the server payload

Configuration lives in server.toml (see 'maktaba-server setup').`
