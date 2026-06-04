package main

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// The migrate / adduser / keys subcommands already exist in the api
// binary. Rather than duplicate goose wiring and the argon2id user
// bootstrap here (and risk drift), the unified binary delegates to
// `maktaba-api <sub> <args...>`, forwarding stdio so prompts and output
// pass through unchanged. DisableFlagParsing hands every flag straight
// to the child so we never have to mirror its flag set.

func newMigrateCmd() *cobra.Command {
	return delegateCmd("migrate", "Run database migrations (delegates to maktaba-api)")
}

func newAddUserCmd() *cobra.Command {
	return delegateCmd("adduser", "Seed the first admin user (delegates to maktaba-api)")
}

func newKeysCmd() *cobra.Command {
	return delegateCmd("keys", "Generate or rotate JWT signing keys (delegates to maktaba-api)")
}

func delegateCmd(sub, short string) *cobra.Command {
	return &cobra.Command{
		Use:                sub,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return runAPISub(sub, args)
		},
	}
}

// runAPISub execs `maktaba-api <sub> <args...>` with the config-derived
// environment layered on so DATABASE_URL etc. flow through even when the
// operator only configured server.toml.
func runAPISub(sub string, args []string) error {
	bin, err := supervisor.LocateAPI("")
	if err != nil {
		return err
	}
	cfg, _, _ := config.Load()

	child := exec.Command(bin, append([]string{sub}, args...)...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	// Config env first, real env last so an explicit shell override wins.
	child.Env = append(supervisor.ChildEnv(supervisor.RoleAPI, cfg), os.Environ()...)
	return child.Run()
}
