package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	componentsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
	catalog "github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Exit codes of `maktaba-api components` (and `maktaba-server components`, which passes
// them through).
const (
	exitComponentsFailed     = 1 // a refusal, a bad argument or an error
	exitComponentsNeedYes    = 2 // a write with no terminal to ask on and no --yes
	exitComponentsNotRunning = 3 // the server (its database) does not answer
)

const componentsUsage = `Usage:
  maktaba-server components list [--json]                   id, group, want, phase, reason
  maktaba-server components show <id> [--json]              conditions per machine, requires, dependents
  maktaba-server components enable <id> [--with-conflicts] [--yes]
  maktaba-server components disable <id> [--yes]
  maktaba-server components stop <id> [--yes]               stopped until start (a hold has no end time)
  maktaba-server components start <id>
  maktaba-server components install <id>                    put it on disk, off
  maktaba-server components remove <id> [--yes]             take it off disk
  maktaba-server components reset-settings <id> [--yes]     its settings back to the defaults
  maktaba-server components import <file> [--yes]           install what an offline file holds (off)
  maktaba-server components offline [<full|core|worker> | <id>...] [--platform=<os-arch>] [--out=<path>]
                                                            make an offline file on this machine and save it
  maktaba-server components bundle [<full|core|worker>] [--yes]   no name: the bundle this home is based on

Every write shows what it changes first and asks, unless --yes. --dsn overrides $DATABASE_URL.`

// componentsService is what the CLI asks: *components.Service is one.
type componentsService interface {
	List(ctx context.Context) (components.ListView, error)
	Show(ctx context.Context, id string) (components.Detail, error)
	Preview(ctx context.Context, req components.Request) (components.Preview, error)
	Apply(ctx context.Context, req components.Request, actor components.Actor) (components.Result, error)
	Imported(ctx context.Context, ids []string, file string, actor components.Actor) (components.Result, error)
	Record(ctx context.Context, actor components.Actor, action, target string, payload map[string]any) error
}

// componentsPacks is this machine's pack installer (its supervisor): *componentsh.Packs is one.
type componentsPacks interface {
	ProgressOf(ctx context.Context, ids []string) map[string]*components.InstallProgress
	ImportPath(ctx context.Context, path string) ([]string, error)
	MakeOffline(ctx context.Context, req componentsh.OfflineRequest) (string, error)
	FetchOffline(ctx context.Context, name string, header http.Header) (*http.Response, *components.InstallProgress, error)
}

// settingsService is what reset-settings asks: *components.Service is one.
type settingsService interface {
	SettingsOf(ctx context.Context, id string) (components.SettingsReset, error)
	ResetSettings(ctx context.Context, id string, actor components.Actor) (components.SettingsReset, error)
}

type componentsCLI struct {
	svc      componentsService
	settings settingsService
	packs    componentsPacks // nil: no supervisor answers on this machine
	in       io.Reader
	out      io.Writer
	errOut   io.Writer
	tty      bool              // there is a terminal to ask on
	user     string            // the OS user: the audit's actor
	values   map[string]string // --out, --platform
	wait     func(time.Duration)
}

// runComponents is `maktaba-api components …`: the components' desired state from the
// command line, through the same service (checks, effect, audit rows) as the admin
// routes, with actor_source cli. It answers the exit code.
func runComponents(argv []string) int {
	flags, args := splitComponentsArgs(argv)
	if flags["help"] || len(args) == 0 {
		fmt.Fprintln(os.Stderr, componentsUsage)
		if flags["help"] {
			return 0
		}
		return exitComponentsFailed
	}
	dsn := componentsDSN(argv)
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "components: no database: pass --dsn or set DATABASE_URL")
		return exitComponentsFailed
	}
	limit := 5 * time.Minute
	if args[0] == "import" || args[0] == "offline" {
		limit = 12 * time.Hour // gigabytes, on this machine's disk
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	db, err := sql.Open("postgres", pgdsn.ForLibPQ(ctx, dsn, pgdsn.Ping))
	if err != nil {
		fmt.Fprintf(os.Stderr, "components: %v\n", err)
		return exitComponentsFailed
	}
	defer db.Close()
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	err = db.PingContext(pingCtx)
	pingCancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "The Maktaba server is not running (its database does not answer): start it, then try again.")
		return exitComponentsNotRunning
	}
	s := components.NewService(db)
	lic := subscriptions.NewStore()
	if v, err := subscriptions.EmbeddedVerifier(); err == nil {
		if st, err := subscriptions.NewPersistentStore(ctx, subscriptions.NewSQLLicensePersistence(db), v); st != nil && err == nil {
			lic = st
		}
	}
	s.Licensed = func(flag string) bool { return lic.Allows(subscriptions.Feature(flag)) }
	s.Tier = lic.TierName
	// a row this command creates says what the licence allows (story 51.19)
	s.AfterApply = func(ctx context.Context) { _, _ = s.PublishLicences(ctx, lic) }
	name := "cli"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	c := &componentsCLI{svc: s, settings: s, in: os.Stdin, out: os.Stdout, errOut: os.Stderr,
		tty: term.IsTerminal(int(os.Stdin.Fd())), user: name, values: componentsValues(argv), wait: time.Sleep}
	// the supervisor maktaba-server names in the environment it passes (nil: none)
	if pk := componentsh.PacksFromEnv(); pk != nil {
		c.packs = pk
	}
	return c.run(ctx, flags, args)
}

// componentsValueFlags are the flags that take a value (--name value or --name=value).
var componentsValueFlags = []string{"dsn", "out", "platform"}

// valueFlag reports whether a is a value flag, and whether its value is the next word.
func valueFlag(a string) (name string, next bool, ok bool) {
	t := strings.TrimLeft(a, "-")
	if t == a {
		return "", false, false
	}
	n, _, hasEq := strings.Cut(t, "=")
	for _, f := range componentsValueFlags {
		if n == f {
			return f, !hasEq, true
		}
	}
	return "", false, false
}

// splitComponentsArgs separates the flags (anywhere on the line) from the words.
func splitComponentsArgs(argv []string) (map[string]bool, []string) {
	flags := map[string]bool{}
	var args []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if _, next, ok := valueFlag(a); ok {
			if next {
				i++ // its value
			}
			continue
		}
		switch {
		case a == "-h" || a == "--help" || a == "-help":
			flags["help"] = true
		case strings.HasPrefix(a, "-"):
			flags[strings.TrimLeft(a, "-")] = true
		default:
			args = append(args, a)
		}
	}
	return flags, args
}

func componentsDSN(argv []string) string { return componentsValues(argv)["dsn"] }

// componentsValues are the value flags of argv.
func componentsValues(argv []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(argv); i++ {
		name, next, ok := valueFlag(argv[i])
		switch {
		case !ok:
		case next && i+1 < len(argv):
			out[name] = argv[i+1]
			i++
		case !next:
			_, v, _ := strings.Cut(argv[i], "=")
			out[name] = v
		}
	}
	return out
}

var componentsFlags = map[string]bool{"json": true, "yes": true, "y": true, "with-conflicts": true, "purge": true}

func (c *componentsCLI) run(ctx context.Context, flags map[string]bool, args []string) int {
	for f := range flags {
		if !componentsFlags[f] {
			return c.usage("unknown flag --" + f)
		}
	}
	yes := flags["yes"] || flags["y"]
	verb, rest := args[0], args[1:]
	one := func() (string, bool) {
		if len(rest) != 1 {
			return "", false
		}
		return rest[0], true
	}
	switch verb {
	case "list":
		if len(rest) != 0 {
			return c.usage("list takes no component")
		}
		return c.list(ctx, flags["json"])
	case "show":
		id, ok := one()
		if !ok {
			return c.usage("show <id>")
		}
		return c.show(ctx, id, flags["json"])
	case "enable", "disable", "stop", "start", "install", "remove":
		id, ok := one()
		if !ok {
			return c.usage(verb + " <id>")
		}
		if verb == "remove" && flags["purge"] {
			fmt.Fprintln(c.errOut, "components remove --purge: available from the release with packs")
			return exitComponentsFailed
		}
		ch := components.Change{ID: id}
		on, off := true, false
		switch verb {
		case "enable":
			ch.Want = catalog.WantEnabled
		case "disable":
			ch.Want = catalog.WantInstalled
		case "remove":
			ch.Want = catalog.WantAbsent
		case "install":
			d, err := c.svc.Show(ctx, id)
			if err != nil {
				return c.fail(err)
			}
			if d.Want != catalog.WantAbsent {
				fmt.Fprintf(c.out, "%s is installed already (%s).\n", id, d.Want)
				return 0
			}
			ch.Want = catalog.WantInstalled
		case "stop":
			ch.Hold = &on
		case "start":
			ch.Hold = &off
		}
		req := components.Request{Changes: []components.Change{ch}, WithConflicts: flags["with-conflicts"]}
		// start only brings back what was stopped: nothing to ask
		return c.write(ctx, req, yes || verb == "start" || verb == "install")
	case "reset-settings":
		id, ok := one()
		if !ok {
			return c.usage("reset-settings <id>")
		}
		return c.resetSettings(ctx, id, yes)
	case "import":
		if len(rest) != 1 {
			return c.usage("import <file>")
		}
		return c.importFile(ctx, rest[0], yes)
	case "offline":
		return c.offline(ctx, rest)
	case "bundle":
		switch len(rest) {
		case 0:
			return c.bundle(ctx)
		case 1:
			return c.write(ctx, components.Request{Bundle: rest[0]}, yes)
		}
		return c.usage("bundle [<full|core|worker>]")
	}
	return c.usage("unknown command " + verb)
}

func (c *componentsCLI) usage(msg string) int {
	fmt.Fprintf(c.errOut, "components: %s\n\n%s\n", msg, componentsUsage)
	return exitComponentsFailed
}

func (c *componentsCLI) fail(err error) int {
	if r, ok := components.AsRefusal(err); ok {
		fmt.Fprintf(c.errOut, "Refused (%s): %s\n", r.Code, r.Detail)
		if len(r.RequiredBy) > 0 {
			fmt.Fprintf(c.errOut, "  Required by: %s\n", strings.Join(r.RequiredBy, ", "))
		}
		if len(r.Conflicts) > 0 {
			fmt.Fprintf(c.errOut, "  Conflicts with: %s (add --with-conflicts to turn them off too)\n", strings.Join(r.Conflicts, ", "))
		}
		if r.Code == components.CodeLicence {
			f := subscriptions.Feature(r.Licence)
			fmt.Fprintf(c.errOut, "  Needs the %s plan. This home is on %s. Apply a licence in Settings → Subscription.\n",
				subscriptions.TierLabel(string(subscriptions.RequiredTier(f))), subscriptions.TierLabel(r.Tier))
			if len(r.Components) > 0 {
				fmt.Fprintf(c.errOut, "  Not in this home's plan: %s\n", strings.Join(r.Components, ", "))
			}
		}
		return exitComponentsFailed
	}
	if errors.Is(err, components.ErrUnknown) {
		fmt.Fprintln(c.errOut, "Refused (unknown_component): no such component")
		return exitComponentsFailed
	}
	fmt.Fprintf(c.errOut, "components: %v\n", err)
	return exitComponentsFailed
}

func (c *componentsCLI) json(v any) int {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return c.fail(err)
	}
	return 0
}

func (c *componentsCLI) list(ctx context.Context, asJSON bool) int {
	l, err := c.svc.List(ctx)
	if err != nil {
		return c.fail(err)
	}
	c.withProgress(ctx, l.Components)
	if asJSON {
		return c.json(l)
	}
	tw := tabwriter.NewWriter(c.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tGROUP\tWANT\tPHASE\tREASON")
	for _, v := range l.Components {
		want := v.Want
		if v.Hold != nil {
			want += " (stopped)"
		}
		phase, reason := v.Phase, v.Reason
		if p := v.Progress; p != nil && p.Busy {
			phase = fmt.Sprintf("%s %d%%", p.Phase, p.Percent)
		} else if p != nil && p.Reason != "" {
			reason = p.Reason
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", v.ID, v.Group, want, phase, reason)
	}
	if err := tw.Flush(); err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.out, bundleWords(l.Bundle))
	return 0
}

func (c *componentsCLI) show(ctx context.Context, id string, asJSON bool) int {
	d, err := c.svc.Show(ctx, id)
	if err != nil {
		return c.fail(err)
	}
	if c.packs != nil && d.Kind == catalog.KindAsset {
		d.Progress = c.packs.ProgressOf(ctx, []string{d.ID})[d.ID]
	}
	if asJSON {
		return c.json(d)
	}
	fmt.Fprintf(c.out, "%s (%s, %s)\n", d.ID, d.Kind, d.Group)
	fmt.Fprintf(c.out, "  want:       %s (generation %d)\n", d.Want, d.Generation)
	if d.Hold != nil {
		fmt.Fprintf(c.out, "  stopped:    by %s at %s, until it is started\n", d.Hold.By, d.Hold.At.Format(time.RFC3339))
	}
	fmt.Fprintf(c.out, "  phase:      %s %s\n", d.Phase, d.Reason)
	if p := d.Progress; p != nil {
		fmt.Fprintf(c.out, "  on disk:    %s\n", progressWords(p))
	}
	var req []string
	for _, g := range d.Requires {
		req = append(req, strings.Join(g, " or "))
	}
	fmt.Fprintf(c.out, "  requires:   %s\n", orNone(req))
	fmt.Fprintf(c.out, "  dependents: %s\n", orNone(d.Dependents))
	if len(d.Conflicts) > 0 {
		fmt.Fprintf(c.out, "  conflicts:  %s\n", strings.Join(d.Conflicts, ", "))
	}
	if d.Licence != "" {
		fmt.Fprintf(c.out, "  licence:    %s (licensed: %v)\n", d.Licence, d.Licensed)
	}
	for _, m := range d.Machines {
		fmt.Fprintf(c.out, "  machine %s: %s %s\n", m.Machine, m.Phase, m.Reason)
		for _, p := range m.Parts {
			fmt.Fprintf(c.out, "    %s: %s\n", p.Part, p.Phase)
			for _, cond := range p.Conditions {
				fmt.Fprintf(c.out, "      %s=%s %s\n", cond.Type, cond.Status, cond.Reason)
			}
		}
	}
	return 0
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

func bundleWords(b components.BundleLine) string {
	s := "Based on " + b.Base
	if len(b.Plus) > 0 {
		s += ", plus " + strings.Join(b.Plus, ", ")
	}
	if len(b.Minus) > 0 {
		s += ", minus " + strings.Join(b.Minus, ", ")
	}
	return s + "."
}

func (c *componentsCLI) bundle(ctx context.Context) int {
	l, err := c.svc.List(ctx)
	if err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.out, bundleWords(l.Bundle))
	return 0
}

// write shows the effect of req, asks (unless yes), and applies it.
func (c *componentsCLI) write(ctx context.Context, req components.Request, yes bool) int {
	p, err := c.svc.Preview(ctx, req)
	if err != nil {
		return c.fail(err)
	}
	if len(p.Refused) > 0 {
		r := p.Refused[0]
		return c.fail(&r)
	}
	if len(p.Effects) == 0 && req.Bundle == "" {
		fmt.Fprintln(c.out, "Nothing changes.")
	} else {
		fmt.Fprintln(c.out, "This changes:")
		for _, e := range p.Effects {
			line := fmt.Sprintf("  %s: %s -> %s", e.ID, e.From, e.To)
			if e.Reason != "" {
				line += " (" + e.Reason + ")"
			}
			fmt.Fprintln(c.out, line)
		}
		if len(p.Conflicts) > 0 {
			fmt.Fprintf(c.out, "  turned off with it: %s\n", strings.Join(p.Conflicts, ", "))
		}
		if p.QueuedJobsParked > 0 {
			fmt.Fprintf(c.out, "  %d queued jobs wait until it is back\n", p.QueuedJobsParked)
		}
	}
	if !yes {
		if !c.tty {
			fmt.Fprintln(c.errOut, "components: no terminal to ask on: add --yes to make the change")
			return exitComponentsNeedYes
		}
		fmt.Fprint(c.out, "Continue? [y/N] ")
		line, _ := bufio.NewReader(c.in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(c.out, "Nothing changed.")
			return exitComponentsFailed
		}
	}
	res, err := c.svc.Apply(ctx, req, components.Actor{By: c.user, Source: "cli"})
	if err != nil {
		return c.fail(err)
	}
	for _, d := range res.Changed {
		state := d.Want
		if d.Hold != nil {
			state += ", stopped until started"
		}
		fmt.Fprintf(c.out, "%s: %s (generation %d)\n", d.ID, state, d.Generation)
	}
	fmt.Fprintln(c.out, "Done.")
	return 0
}

// resetSettings is `components reset-settings <id>` (story 51.13): what a reset loses,
// asked first unless yes, then the component's rules sections back to the defaults.
func (c *componentsCLI) resetSettings(ctx context.Context, id string, yes bool) int {
	if c.settings == nil {
		return c.fail(errors.New("no settings service"))
	}
	cur, err := c.settings.SettingsOf(ctx, id)
	if err != nil {
		return c.fail(err)
	}
	if len(cur.Sections) == 0 {
		fmt.Fprintf(c.out, "%s has no settings of its own.\n", id)
		return 0
	}
	fmt.Fprintf(c.out, "Reset %s's settings to the defaults? Your changes to %d settings will be lost.\n", id, cur.Changed)
	if !yes {
		if !c.tty {
			fmt.Fprintln(c.errOut, "components: no terminal to ask on: add --yes to make the change")
			return exitComponentsNeedYes
		}
		fmt.Fprint(c.out, "Continue? [y/N] ")
		line, _ := bufio.NewReader(c.in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(c.out, "Nothing changed.")
			return exitComponentsFailed
		}
	}
	res, err := c.settings.ResetSettings(ctx, id, components.Actor{By: c.user, Source: "cli"})
	if err != nil {
		return c.fail(err)
	}
	for _, sec := range res.Sections {
		fmt.Fprintf(c.out, "rules.%s: defaults (%d changed settings deleted)\n", sec.Section, sec.Changed)
	}
	fmt.Fprintln(c.out, "Done.")
	return 0
}
