package main

import (
	"context"
	"errors"
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

func newLicenseCmd() *cobra.Command {
	return delegateCmd("license", "Apply or show the home's license (delegates to maktaba-api)")
}

func newKeysCmd() *cobra.Command {
	return delegateCmd("keys", "Generate or rotate JWT signing keys (delegates to maktaba-api)")
}

// newComponentsCmd is `maktaba-server components …` (Epic 51 story 51.08). Its exit
// codes mean something to scripts (2: a write with no terminal and no --yes, 3: the
// server is not running), so the child's code is this process's.
func newComponentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "components",
		Short:              "List, turn on, off, stop or start the components, import or make an offline file (delegates to maktaba-api)",
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			err := runAPISub("components", args)
			if code, ok := childExitCode(err); ok {
				os.Exit(code)
			}
			return err
		},
	}
}

// childExitCode is the exit code of a child that ran and failed.
func childExitCode(err error) (int, bool) {
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() > 0 {
		return ee.ExitCode(), true
	}
	return 0, false
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
	if _, err := bundledPostgres(context.Background(), &cfg, false); err != nil {
		return err
	}

	child := exec.Command(bin, append([]string{sub}, args...)...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	// Config env first, real env last so an explicit shell override wins.
	env, err := supervisor.ChildEnv(supervisor.RoleAPI, cfg)
	if err != nil {
		return err
	}
	child.Env = append(env, os.Environ()...)
	return child.Run()
}
