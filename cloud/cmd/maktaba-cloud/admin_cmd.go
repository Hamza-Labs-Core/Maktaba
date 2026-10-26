package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// runAdmin manages the service-admin role from the operator's shell:
//
//	maktaba-cloud admin grant  --email ops@example.com
//	maktaba-cloud admin revoke --email ops@example.com
//	maktaba-cloud admin list
//
// This is the only way to grant the role: no HTTP endpoint can, so a
// compromised account cannot promote itself.
func runAdmin(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: maktaba-cloud admin grant|revoke|list [--email addr] [--config path]")
		os.Exit(2)
	}
	sub := args[0]
	fs := flag.NewFlagSet("admin "+sub, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cloud.toml")
	email := fs.String("email", "", "account email")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(2)
	}
	logger := newLogger()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("config load", "err", err)
		os.Exit(2)
	}
	pool, err := db.Open(cfg.Database.URL, 2, 1, time.Minute)
	if err != nil {
		logger.Error("db open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := adminRole(ctx, stores.NewUsers(pool.DB), audit.New(pool.DB), sub, *email, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "admin "+sub+":", err)
		os.Exit(1)
	}
}

// adminRole carries out one admin subcommand.
func adminRole(ctx context.Context, users *stores.Users, log *audit.Log, sub, email string, out io.Writer) error {
	switch sub {
	case "list":
		list, err := users.ServiceAdmins(ctx)
		if err != nil {
			return err
		}
		for _, a := range list {
			fmt.Fprintf(out, "%s\t%s\t%s\n", a.ID, a.Email, a.Status)
		}
		return nil
	case "grant", "revoke":
	default:
		return fmt.Errorf("unknown subcommand %q (grant, revoke, list)", sub)
	}
	if email == "" {
		return errors.New("--email is required")
	}
	u, err := users.ByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("no account %s: %w", email, err)
	}
	role, action := stores.RoleUser, audit.ActionRoleRevoke
	if sub == "grant" {
		if !u.EmailVerified {
			return errors.New("the account's email is not verified; verify it first")
		}
		if u.Status != stores.StatusActive {
			return errors.New("the account is suspended")
		}
		role, action = stores.RoleServiceAdmin, audit.ActionRoleGrant
	}
	if err := users.SetRole(ctx, u.ID, role); err != nil {
		return err
	}
	if err := log.Write(ctx, audit.Entry{ActorEmail: "cli", Action: action, TargetKind: "user", TargetID: u.ID,
		Detail: map[string]any{"email": u.Email, "role": role}}); err != nil {
		return fmt.Errorf("role changed but the audit row failed: %w", err)
	}
	fmt.Fprintf(out, "%s is now %s\n", u.Email, role)
	return nil
}
