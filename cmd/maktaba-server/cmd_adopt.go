package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/adopt"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 38 story 38.13: a Maktaba this PC already runs (a scheduled task, a
// script, the node installer's workers) moves into the desktop app. The move
// itself is the app's setup screen (it then registers and starts the app's
// server); this command shows what it finds and plans, verifies, and goes back.
func newAdoptCmd() *cobra.Command {
	var plan string
	var verify, undo bool
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Show the Maktaba this PC runs outside the app, or undo its move into the app",
		Long: `adopt lists the Maktaba installs this PC runs outside the desktop app (its scheduled tasks
and running servers) and what moving one into the app carries (--plan <id>). The app's setup
offers the move; once the app's server ran healthy the old task is removed (--verify does it now).
--undo stops the app's server and runs the old install again from its saved task.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			sys := adoptSys()
			tgt := adoptTarget(cfg)
			payload, _ := setupPayload()
			switch {
			case undo:
				if err := adopt.Undo(sys, tgt); err != nil {
					return err
				}
				fmt.Println("The old install runs again. The app's server is stopped and its task turned off; the app opens its setup, which offers the move again.")
				return nil
			case verify:
				if err := adopt.Verify(ctx, tgt, cfg, wait); err != nil {
					return err
				}
				if _, err := adopt.Finish(sys, tgt.StateDir); err != nil {
					return err
				}
				fmt.Println("Verified: the old task is removed (its definition stays in", filepath.Dir(adopt.RecordPath(tgt.StateDir))+").")
				return nil
			case plan != "":
				src, err := adoptSource(sys, payload, tgt, plan)
				if err != nil {
					return err
				}
				p, err := adopt.MakePlan(sys, src, tgt)
				if err != nil {
					return err
				}
				printAdoptPlan(p)
				if len(p.Problems) > 0 {
					return errors.New("it cannot move as it is")
				}
				return nil
			}
			srcs, err := adopt.Find(sys, payload)
			if err != nil {
				return err
			}
			if rec, ok, err := adopt.ReadRecord(tgt.StateDir); err == nil && ok {
				fmt.Printf("A move into the app is recorded: %s (%s), %s\n\n", rec.Source.ID, rec.Source.Kind, rec.Stage)
			}
			if len(srcs) == 0 {
				fmt.Println("No Maktaba runs on this PC outside the app.")
				return nil
			}
			for _, s := range srcs {
				printAdoptSource(s)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&plan, "plan", "", "what moving the install with this id (from the list) does; changes nothing")
	f.BoolVar(&verify, "verify", false, "check the app's server after a move and remove the old task")
	f.BoolVar(&undo, "undo", false, "stop the app's server and run the install moved into it again")
	f.DurationVar(&wait, "wait", adoptVerifyWait, "with --verify: how long to wait for the app's server")
	cmd.MarkFlagsMutuallyExclusive("plan", "verify", "undo")
	return cmd
}

func printAdoptSource(s adopt.Source) {
	fmt.Printf("%s\n  kind:   %s\n", s.ID, s.Kind)
	if s.Script != "" {
		fmt.Printf("  script: %s\n", s.Script)
	}
	fmt.Printf("  folder: %s\n", s.Dir)
	if s.Config != "" {
		state := "missing"
		if s.ConfigFound {
			state = "found"
		}
		fmt.Printf("  config: %s (%s)\n", s.Config, state)
	}
	switch s.DB {
	case adopt.DBNode:
		fmt.Printf("  role:   a node of the home %s (%s)\n", s.Home, s.NodeConfig)
	case adopt.DBOwnPostgres, adopt.DBPostgres:
		fmt.Printf("  database: Postgres at %s\n", s.DBWhere)
	default:
		fmt.Printf("  database: SQLite\n")
	}
	for _, name := range []string{"web", "api", "stream"} {
		if addr := s.Ports[name]; addr != "" {
			fmt.Printf("  %-7s %s\n", name+":", addr)
		}
	}
	if len(s.PIDs) > 0 {
		fmt.Printf("  running: %d process(es)\n", len(s.PIDs))
	}
	fmt.Println()
}

func printAdoptPlan(p adopt.Plan) {
	printAdoptSource(p.Source)
	if len(p.Carried) > 0 {
		fmt.Println("Carried into the app's server.toml:", strings.Join(p.Carried, ", "))
	}
	for _, n := range p.NotCarried {
		fmt.Println("Not carried:", n)
	}
	for _, it := range p.Items {
		state := ""
		if it.Done {
			state = " (in the app already)"
		}
		fmt.Printf("Copied: %-22s %s -> %s, %d bytes%s\n", it.What, it.From, it.To, it.Size, state)
	}
	for _, w := range p.Warnings {
		fmt.Println("Note:", w)
	}
	for _, pr := range p.Problems {
		fmt.Println("Problem:", pr)
	}
}
