package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/devmigrate"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// Epic 36 story 36.9: a Mac that runs the developer stack moves into Maktaba.app.
func newMigrateFromDevCmd() *cobra.Command {
	var dryRun, verify, finalize, yes, replace bool
	var container, repo, payload string
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "migrate-from-dev",
		Short: "Move this Mac's developer stack (stack.sh + Docker Postgres) into Maktaba.app",
		Long: `migrate-from-dev moves a Mac that runs tools/dev/stack.sh and the compose Postgres in
Docker into Maktaba.app: it stops the stack, dumps the Docker database into the bundled Postgres,
copies the keys, the Chroma index and the models the pipeline loads into the app's folder, and
carries the stack's path map and transcription settings into server.toml. Run it from the app's
payload (/Applications/Maktaba.app/Contents/Resources/server/bin/maktaba-server) with Maktaba quit.

Nothing of the stack is deleted: the container is stopped with its volume kept, and files are
copied (APFS clones). Then open Maktaba and run --verify; --finalize retires the Docker database.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			t, err := devTarget(payload)
			if err != nil {
				return err
			}
			m := &devmigrate.Migration{Sys: devmigrate.RealSys(), Target: t, Container: container, Repo: repo,
				Out: os.Stdout, Replace: replace}
			switch {
			case verify:
				return m.Verify(ctx, wait)
			case finalize:
				return m.Finalize(ctx, func(prompt string) bool { return yes || ask(prompt) == "retire" })
			}
			p := m.MakePlan(ctx)
			if dryRun {
				fmt.Println("Moving this Mac's developer stack into Maktaba.app (dry run: nothing changes)")
				fmt.Println()
			}
			m.Print(p)
			if len(p.Problems) > 0 {
				return errors.New("nothing was changed")
			}
			if dryRun {
				return nil
			}
			if !yes && !strings.HasPrefix(strings.ToLower(ask("\nGo ahead? [y/N] ")), "y") {
				return errors.New("nothing was changed")
			}
			return m.Run(ctx, p)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "print the plan with sizes; change nothing")
	f.BoolVar(&verify, "verify", false, "after Maktaba started: check the API, the counts and the Live TV host check-in")
	f.BoolVar(&finalize, "finalize", false, "after --verify: delete the Docker container and its volume (asks first)")
	f.BoolVarP(&yes, "yes", "y", false, "do not ask")
	f.BoolVar(&replace, "replace", false, "go ahead over an app install that has a server.toml or database already (they are replaced)")
	f.StringVar(&container, "container", devmigrate.DefaultContainer, "the Docker container of the stack's Postgres")
	f.StringVar(&repo, "dev-repo", "", "the checkout whose tools/dev/stack.sh runs the stack (default: found from the running stack)")
	f.StringVar(&payload, "payload", "", "the app's server payload (default: the one this binary runs from)")
	f.DurationVar(&wait, "wait", 3*time.Minute, "with --verify: how long to wait for the app to come up")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "verify", "finalize")
	return cmd
}

// devTarget is Maktaba.app's side of the move: server.toml, the state root and
// the bundled Postgres of the payload this binary runs from.
func devTarget(payload string) (devmigrate.Target, error) {
	cfg, found, err := config.Load()
	if err != nil {
		return devmigrate.Target{}, err
	}
	t := devmigrate.Target{Config: cfg, ConfigPath: config.Path(), Found: found, State: cfg.StateDir(), Keys: config.KeysDir()}
	if payload == "" {
		payload = config.AppBundle()
	}
	if payload == "" {
		if exe, err := os.Executable(); err == nil {
			if r, err := filepath.EvalSymlinks(exe); err == nil {
				exe = r
			}
			payload = filepath.Dir(filepath.Dir(exe)) // <payload>/bin/maktaba-server
		}
	}
	t.Payload = payload
	switch {
	case cfg.DatabaseURLSet:
		t.NoBundle = "server.toml sets [database] url (remove it to use the bundled Postgres)"
		return t, nil
	case strings.EqualFold(strings.TrimSpace(cfg.Postgres.Enabled), "off"):
		t.NoBundle = "server.toml sets [postgres] enabled = \"off\""
		return t, nil
	}
	on := cfg
	on.Postgres.Enabled = "on"
	b, ok, err := pgbundle.Resolve(on, filepath.Join(payload, "bin", "maktaba-server"))
	if err != nil || !ok {
		t.NoBundle = "run this from Maktaba.app: /Applications/Maktaba.app/Contents/Resources/server/bin/maktaba-server"
		return t, nil
	}
	t.Bundle = &b
	return t, nil
}

func ask(prompt string) string {
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line)
}
