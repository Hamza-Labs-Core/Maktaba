package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// StaffRoles are the roles a staff account can hold (story 50.33 §1). A new role is added
// here and to the routes that require it, each with its role test.
var StaffRoles = []string{stores.StaffRoleServiceAdmin}

const staffUsage = `usage: maktaba-cloud staff <command> [--email addr] [--role name] [--config path]
  add       --email --role   create a staff account; prints a one-time enrolment link (24 h)
  grant     --email --role   add a role
  revoke    --email --role   remove a role
  disable   --email          disable the account and end every session
  enable    --email          enable a disabled account
  reset-mfa --email          remove every factor; prints a new enrolment link
  unlock    --email          clear a lockout early
  list                       every staff account`

// runStaff manages the operator console's staff accounts from the leader's shell (story
// 50.33 §1). No HTTP route creates staff or changes roles: this command is the only way.
func runStaff(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, staffUsage)
		os.Exit(2)
	}
	sub := args[0]
	fs := flag.NewFlagSet("staff "+sub, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cloud.toml")
	email := fs.String("email", "", "staff account email")
	role := fs.String("role", "", "role ("+strings.Join(StaffRoles, ", ")+")")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(2)
	}
	logger := newLogger("staff")
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
	c := staffCmd{Staff: &stores.Staff{DB: pool.DB}, Audit: audit.New(pool.DB), Out: os.Stdout,
		EnrolURL: cfg.Server.AdminOriginOrDefault() + "/enroll#", Now: time.Now}
	if err := c.run(ctx, sub, *email, *role); err != nil {
		fmt.Fprintln(os.Stderr, "staff "+sub+":", err)
		os.Exit(1)
	}
}

// staffCmd carries out one staff subcommand; tests drive it directly.
type staffCmd struct {
	Staff *stores.Staff
	Audit *audit.Log
	Out   io.Writer
	// EnrolURL is the enrolment page, the token appended after its "#" (it stays in the
	// fragment, so no server log or Referer ever carries it).
	EnrolURL string
	Now      func() time.Time
}

func (c staffCmd) run(ctx context.Context, sub, email, role string) error {
	if sub == "list" {
		return c.list(ctx)
	}
	switch sub {
	case "add", "grant", "revoke", "disable", "enable", "reset-mfa", "unlock":
	default:
		return fmt.Errorf("unknown command %q\n%s", sub, staffUsage)
	}
	if strings.TrimSpace(email) == "" {
		return errors.New("--email is required")
	}
	needsRole := sub == "add" || sub == "grant" || sub == "revoke"
	if needsRole && !slices.Contains(StaffRoles, role) {
		return fmt.Errorf("--role must be one of: %s", strings.Join(StaffRoles, ", "))
	}
	now := c.Now()
	if sub == "add" {
		a, err := c.Staff.Create(ctx, email, []string{role})
		if errors.Is(err, stores.ErrStaffExists) {
			return fmt.Errorf("%s already has a staff account (reset-mfa prints a new link)", email)
		}
		if err != nil {
			return err
		}
		if err := c.audit(ctx, audit.ActionStaffAdded, a, map[string]any{"roles": []string{role}}); err != nil {
			return err
		}
		return c.enrolLink(ctx, a, now)
	}
	a, err := c.Staff.ByEmail(ctx, email)
	if errors.Is(err, stores.ErrStaffNotFound) {
		return fmt.Errorf("no staff account %s", email)
	}
	if err != nil {
		return err
	}
	switch sub {
	case "grant", "revoke":
		roles := slices.DeleteFunc(slices.Clone(a.Roles), func(r string) bool { return r == role })
		action := audit.ActionStaffRevoked
		if sub == "grant" {
			roles, action = append(roles, role), audit.ActionStaffGranted
		}
		if err := c.Staff.SetRoles(ctx, a.ID, roles); err != nil {
			return err
		}
		// §4: a role change ends every session of the account
		if err := c.Staff.RevokeAll(ctx, a.ID, "role_change", now); err != nil {
			return err
		}
		if err := c.audit(ctx, action, a, map[string]any{"role": role}); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "%s: roles %s (every session ended)\n", a.Email, strings.Join(roles, ","))
	case "disable", "enable":
		status, action := "disabled", audit.ActionStaffDisabled
		if sub == "enable" {
			status, action = "active", audit.ActionStaffEnabled
		}
		if err := c.Staff.SetStatus(ctx, a.ID, status, now); err != nil {
			return err
		}
		if err := c.audit(ctx, action, a, nil); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "%s is %s\n", a.Email, status)
	case "reset-mfa":
		if err := c.Staff.ResetMFA(ctx, a.ID, now); err != nil {
			return err
		}
		if err := c.audit(ctx, audit.ActionStaffResetMFA, a, nil); err != nil {
			return err
		}
		return c.enrolLink(ctx, a, now)
	case "unlock":
		if err := c.Staff.Unlock(ctx, a.ID); err != nil {
			return err
		}
		if err := c.audit(ctx, audit.ActionStaffUnlocked, a, nil); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "%s is unlocked\n", a.Email)
	}
	return nil
}

func (c staffCmd) list(ctx context.Context) error {
	list, err := c.Staff.List(ctx)
	if err != nil {
		return err
	}
	for _, a := range list {
		enrolled := "enrolled"
		if !a.EnrolledAt.Valid {
			enrolled = "not enrolled"
		}
		fmt.Fprintf(c.Out, "%s\t%s\t%s\t%s\t%s\n", a.ID, a.Email, strings.Join(a.Roles, ","), a.Status, enrolled)
	}
	return nil
}

// enrolLink prints a new one-time enrolment link for a (24 h; any earlier link dies).
func (c staffCmd) enrolLink(ctx context.Context, a stores.StaffAccount, now time.Time) error {
	raw, err := staffauth.NewRawToken()
	if err != nil {
		return err
	}
	if err := c.Staff.NewEnrolment(ctx, a.ID, staffauth.TokenHash(raw), now.Add(staffauth.EnrolTTL)); err != nil {
		return err
	}
	fmt.Fprintf(c.Out, "%s: enrolment link (valid 24 h, one use):\n%s%s\n", a.Email, c.EnrolURL, raw)
	return nil
}

func (c staffCmd) audit(ctx context.Context, action string, a stores.StaffAccount, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["email"] = a.Email
	if err := c.Audit.Write(ctx, audit.Entry{ActorKind: audit.ActorCLI, ActorEmail: "cli", Action: action,
		TargetKind: "staff", TargetID: a.ID, Detail: detail}); err != nil {
		return fmt.Errorf("changed, but the audit row failed: %w", err)
	}
	return nil
}
