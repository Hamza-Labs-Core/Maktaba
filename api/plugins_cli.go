package main

// `maktaba-api plugins …` and `maktaba-server plugins …`, which passes through to it (Epic 54,
// story 54.10 §4, §5): the admin routes of /api/plugins for a headless home, as the components
// CLI is for /api/components (components_cli.go): the same flags (--yes, --json, --dsn), what
// a write changes shown first with `Continue? [y/N]`, and the same exit codes. Each command
// calls the plugins lifecycle the routes call, so the checks, the audit and the supervisor's
// unpack are the same. `plugins dev <folder>` installs an unpacked folder as a Development
// plugin and reinstalls it whenever a file changes (story 54.10 §4).

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
	catalog "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
)

const pluginsUsage = `Usage:
  maktaba-server plugins list [--json]                  id, version, want, phase, reason
  maktaba-server plugins show <id> [--json]             its detail: grants, stages, health
  maktaba-server plugins install <file.mkplugin> [--name <its name>] [--enable]
                 [--grant <cap,...>] [--library <id,...> | --all-libraries] [--yes]
  maktaba-server plugins update <id> <file.mkplugin> [--name <its name>] [--yes]
  maktaba-server plugins enable|disable|start|stop <id> [--yes]
  maktaba-server plugins rollback <id> [--clear-data] [--yes]
  maktaba-server plugins remove <id> [--purge] [--yes]
  maktaba-server plugins grants <id> [--grant <cap,...>] [--library <id,...> | --all-libraries] [--yes]
  maktaba-server plugins policy [--allow-unreviewed=on|off] [--yes]
  maktaba-server plugins dev <folder> [--keep]          install a folder as Development and reinstall it on
                                                        every change; removed when it stops unless --keep

An unreviewed plugin installs only with its name typed (--name, in English or Arabic), and
only while the home allows unreviewed plugins (plugins policy). Every write shows what it
changes first and asks, unless --yes. --dsn overrides $DATABASE_URL.`

// pluginsOps is what the CLI asks; *pluginsDeps (the lifecycle and the components service)
// is one.
type pluginsOps interface {
	List(ctx context.Context) ([]plugins.View, error)
	Show(ctx context.Context, id string) (*plugins.Detail, error)
	Install(ctx context.Context, req plugins.InstallRequest, actor components.Actor) (*plugins.InstallResult, error)
	SetGrants(ctx context.Context, id string, g plugins.GrantSet, actor components.Actor) (*plugins.GrantSet, error)
	Policy(ctx context.Context) (plugins.Policy, error)
	SetPolicy(ctx context.Context, p plugins.Policy, actor components.Actor) (plugins.Policy, error)
	Rollback(ctx context.Context, id string, clearData bool, actor components.Actor) error
	Apply(ctx context.Context, req components.Request, actor components.Actor) (components.Result, error)
	Purge(ctx context.Context, id string, actor components.Actor) (int, error)
}

type pluginsDeps struct {
	*plugins.Lifecycle
}

func (d pluginsDeps) Policy(ctx context.Context) (plugins.Policy, error) {
	return plugins.LoadPolicy(ctx, d.DB)
}

func (d pluginsDeps) Apply(ctx context.Context, req components.Request, actor components.Actor) (components.Result, error) {
	return d.Components.Apply(ctx, req, actor)
}

// Purge clears what the plugin left as the route's purge=true does: its settings, then its
// data, artifacts and writes.
func (d pluginsDeps) Purge(ctx context.Context, id string, actor components.Actor) (int, error) {
	cid := catalog.PluginPrefix + id
	if _, err := d.Components.ResetSettings(ctx, cid, actor); err != nil {
		return 0, err
	}
	return d.Components.PurgePlugin(ctx, cid, actor)
}

type pluginsCLI struct {
	ops    pluginsOps
	in     io.Reader
	out    io.Writer
	errOut io.Writer
	tty    bool
	user   string
	values map[string]string
	// dev mode: how often the folder is read, and when it stops (nil: SIGINT and SIGTERM)
	poll time.Duration
	stop <-chan struct{}
}

// pluginsValueFlags are the plugins CLI's flags that take a value.
var pluginsValueFlags = []string{"dsn", "name", "grant", "library", "allow-unreviewed"}

// splitPluginsArgs separates the flags (--yes, --name=x, --name x) from the words.
func splitPluginsArgs(argv []string) (map[string]bool, map[string]string, []string) {
	flags, values, args := map[string]bool{}, map[string]string{}, []string{}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			args = append(args, a)
			continue
		}
		t := strings.TrimLeft(a, "-")
		name, val, hasEq := strings.Cut(t, "=")
		isValue := false
		for _, f := range pluginsValueFlags {
			if name == f {
				isValue = true
			}
		}
		switch {
		case isValue && hasEq:
			values[name] = val
		case isValue && i+1 < len(argv):
			values[name] = argv[i+1]
			i++
		case isValue:
			values[name] = ""
		default:
			flags[name] = true
		}
	}
	return flags, values, args
}

func runPlugins(argv []string) int {
	flags, values, args := splitPluginsArgs(argv)
	if flags["help"] || flags["h"] || len(args) == 0 {
		fmt.Fprintln(os.Stderr, pluginsUsage)
		if flags["help"] || flags["h"] {
			return 0
		}
		return exitComponentsFailed
	}
	dsn := values["dsn"]
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "plugins: no database: pass --dsn or set DATABASE_URL")
		return exitComponentsFailed
	}
	ctx := context.Background()
	if args[0] != "dev" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Minute) // an install's preload compiles
		defer cancel()
	}
	db, err := sql.Open("postgres", pgdsn.ForLibPQ(ctx, dsn, pgdsn.Ping))
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugins: %v\n", err)
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
	l := &plugins.Lifecycle{DB: db, Components: s, Version: version.Version}
	// the supervisor maktaba-server names in the environment it passes: it unpacks a
	// package (install, update, dev) as the API's route has it do
	if sup := plugins.SupervisorFromEnv(); sup != nil {
		l.Sup = sup
	}
	s.Plugins = l
	name := "cli"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	c := &pluginsCLI{ops: pluginsDeps{l}, in: os.Stdin, out: os.Stdout, errOut: os.Stderr,
		tty: term.IsTerminal(int(os.Stdin.Fd())), user: name, values: values}
	if l.Sup == nil && (args[0] == "install" || args[0] == "update" || args[0] == "dev") {
		fmt.Fprintln(os.Stderr, "plugins: no supervisor to unpack the package: run it as `maktaba-server plugins …` on the home")
		return exitComponentsFailed
	}
	return c.run(ctx, flags, args)
}

func (c *pluginsCLI) actor() components.Actor { return components.Actor{By: c.user, Source: "cli"} }

// fail prints err (a plugin refusal with its code) and answers the refusal's exit code.
func (c *pluginsCLI) fail(err error) int {
	var p *plugins.Problem
	if errors.As(err, &p) {
		fmt.Fprintf(c.errOut, "plugins: %s: %s\n", p.Code, p.Detail)
		return exitComponentsFailed
	}
	fmt.Fprintf(c.errOut, "plugins: %v\n", err)
	return exitComponentsFailed
}

// confirm asks before a write: 0 to go on, else the exit code.
func (c *pluginsCLI) confirm(yes bool, what string) int {
	fmt.Fprintln(c.out, what)
	if yes {
		return 0
	}
	if !c.tty {
		fmt.Fprintln(c.errOut, "plugins: no terminal to ask on: add --yes to make the change")
		return exitComponentsNeedYes
	}
	fmt.Fprint(c.out, "Continue? [y/N] ")
	line, _ := bufio.NewReader(c.in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		fmt.Fprintln(c.out, "Nothing changed.")
		return exitComponentsFailed
	}
	return 0
}

func (c *pluginsCLI) printJSON(v any) int {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return 0
}

func listOf(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (c *pluginsCLI) run(ctx context.Context, flags map[string]bool, args []string) int {
	yes := flags["yes"] || flags["y"]
	need := func(n int) bool {
		if len(args) != n {
			fmt.Fprintln(c.errOut, pluginsUsage)
			return false
		}
		return true
	}
	switch args[0] {
	case "list":
		return c.list(ctx, flags["json"])
	case "show":
		if !need(2) {
			return exitComponentsFailed
		}
		d, err := c.ops.Show(ctx, args[1])
		if err != nil {
			return c.fail(err)
		}
		if flags["json"] {
			return c.printJSON(d)
		}
		return c.showText(d)
	case "install":
		if !need(2) {
			return exitComponentsFailed
		}
		return c.install(ctx, args[1], "", flags, yes)
	case "update":
		if !need(3) {
			return exitComponentsFailed
		}
		return c.install(ctx, args[2], args[1], flags, yes)
	case "enable", "disable", "start", "stop":
		if !need(2) {
			return exitComponentsFailed
		}
		return c.switchTo(ctx, args[0], args[1], yes)
	case "rollback":
		if !need(2) {
			return exitComponentsFailed
		}
		clearData := flags["clear-data"]
		what := fmt.Sprintf("Roll %s back to its kept version.", args[1])
		if clearData {
			what += " Its stored data is cleared; the tags, chapters and subtitles it wrote are kept."
		}
		if code := c.confirm(yes, what); code != 0 {
			return code
		}
		if err := c.ops.Rollback(ctx, args[1], clearData, c.actor()); err != nil {
			return c.fail(err)
		}
		fmt.Fprintf(c.out, "%s: rolled back.\n", args[1])
		return 0
	case "remove":
		if !need(2) {
			return exitComponentsFailed
		}
		return c.remove(ctx, args[1], flags["purge"], yes)
	case "grants":
		if !need(2) {
			return exitComponentsFailed
		}
		return c.grants(ctx, args[1], flags, yes)
	case "policy":
		return c.policy(ctx, yes)
	case "dev":
		if !need(2) {
			return exitComponentsFailed
		}
		return c.dev(ctx, args[1], flags["keep"])
	}
	fmt.Fprintln(c.errOut, pluginsUsage)
	return exitComponentsFailed
}

func (c *pluginsCLI) list(ctx context.Context, asJSON bool) int {
	v, err := c.ops.List(ctx)
	if err != nil {
		return c.fail(err)
	}
	if asJSON {
		return c.printJSON(v)
	}
	if len(v) == 0 {
		fmt.Fprintln(c.out, "No plugin is installed.")
		return 0
	}
	w := tabwriter.NewWriter(c.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tVERSION\tWANT\tPHASE\tREASON\tREVIEW")
	for _, p := range v {
		reason := p.Reason
		if p.KeptOff != nil {
			reason = strings.TrimPrefix(strings.Join([]string{reason, "kept off"}, ", "), ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Version, p.Want, p.Phase, reason, p.ReviewLevel)
	}
	_ = w.Flush()
	return 0
}

func (c *pluginsCLI) showText(d *plugins.Detail) int {
	fmt.Fprintf(c.out, "%s %s (%s)\n", d.ID, d.Version, d.ReviewLevel)
	if n := d.Names.Name["en"]; n != "" {
		fmt.Fprintf(c.out, "  name: %s\n", n)
	}
	fmt.Fprintf(c.out, "  want: %s, phase: %s", d.Want, d.Phase)
	if d.Reason != "" {
		fmt.Fprintf(c.out, " (%s)", d.Reason)
	}
	fmt.Fprintln(c.out)
	libs := "none"
	switch {
	case d.Grants.AllLibraries:
		libs = "all"
	case len(d.Grants.Libraries) > 0:
		libs = strings.Join(d.Grants.Libraries, ", ")
	}
	fmt.Fprintf(c.out, "  granted: %s; libraries: %s\n", strings.Join(d.Grants.Capabilities, ", "), libs)
	fmt.Fprintf(c.out, "  jobs: %d done, %d failed\n", d.Health.JobsDone, d.Health.JobsFailed)
	for _, s := range d.Stages {
		fmt.Fprintf(c.out, "  stage %s (%s): %d queued, %d running\n", s.Name, s.Subject, s.Pending, s.Running)
	}
	return 0
}

func (c *pluginsCLI) install(ctx context.Context, file, expect string, flags map[string]bool, yes bool) int {
	pkg, err := os.ReadFile(file)
	if err != nil {
		return c.fail(err)
	}
	req := plugins.InstallRequest{Package: bytes.NewReader(pkg), ExpectID: expect, Enable: flags["enable"],
		ConfirmName: c.values["name"]}
	if _, ok := c.values["grant"]; ok || flags["all-libraries"] || c.values["library"] != "" {
		req.GrantsSet = true
		req.Grants = listOf(c.values["grant"])
		req.Libraries = listOf(c.values["library"])
		req.AllLibraries = flags["all-libraries"]
	}
	what := fmt.Sprintf("Install %s.", file)
	if expect != "" {
		what = fmt.Sprintf("Update %s from %s (the current version is kept for a rollback).", expect, file)
	}
	if req.GrantsSet {
		what += fmt.Sprintf(" Grant: %s.", strings.Join(append([]string{}, req.Grants...), ", "))
	}
	if req.Enable {
		what += " Then turn it on."
	}
	if code := c.confirm(yes, what); code != 0 {
		return code
	}
	res, err := c.ops.Install(ctx, req, c.actor())
	if err != nil {
		return c.fail(err)
	}
	switch {
	case res.Already:
		fmt.Fprintf(c.out, "%s %s is installed already: nothing changed.\n", res.PluginID, res.Version)
	case res.Updated:
		fmt.Fprintf(c.out, "%s updated to %s.\n", res.PluginID, res.Version)
	default:
		fmt.Fprintf(c.out, "%s %s installed.\n", res.PluginID, res.Version)
	}
	if res.EnableError != "" {
		fmt.Fprintf(c.errOut, "plugins: not turned on: %s\n", res.EnableError)
		return exitComponentsFailed
	}
	return 0
}

func (c *pluginsCLI) switchTo(ctx context.Context, action, id string, yes bool) int {
	ch := components.Change{ID: catalog.PluginPrefix + id}
	what := ""
	switch action {
	case "enable":
		ch.Want, what = catalog.WantEnabled, "Turn "+id+" on."
	case "disable":
		ch.Want, what = catalog.WantInstalled, "Turn "+id+" off (it stays off through updates until an admin turns it on)."
	case "stop":
		on := true
		ch.Hold, what = &on, "Stop "+id+" until it is started (its queued jobs wait)."
	case "start":
		off := false
		ch.Hold, what = &off, "Start "+id+"."
	}
	if code := c.confirm(yes, what); code != 0 {
		return code
	}
	if _, err := c.ops.Apply(ctx, components.Request{Changes: []components.Change{ch}}, c.actor()); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.out, "%s: done.\n", id)
	return 0
}

func (c *pluginsCLI) remove(ctx context.Context, id string, purge, yes bool) int {
	if _, err := c.ops.Show(ctx, id); err != nil {
		return c.fail(err)
	}
	what := "Remove " + id + ": its queued jobs are deleted; its data and settings are kept."
	if purge {
		what = "Remove " + id + " and clear what it left: its settings, its data, and the writes no one changed since."
	}
	if code := c.confirm(yes, what); code != 0 {
		return code
	}
	if _, err := c.ops.Apply(ctx, components.Request{Changes: []components.Change{{ID: catalog.PluginPrefix + id,
		Want: catalog.WantAbsent}}}, c.actor()); err != nil {
		return c.fail(err)
	}
	if purge {
		n, err := c.ops.Purge(ctx, id, c.actor())
		if err != nil {
			return c.fail(err)
		}
		fmt.Fprintf(c.out, "%s removed; %d writes reverted.\n", id, n)
		return 0
	}
	fmt.Fprintf(c.out, "%s removed.\n", id)
	return 0
}

func (c *pluginsCLI) grants(ctx context.Context, id string, flags map[string]bool, yes bool) int {
	d, err := c.ops.Show(ctx, id)
	if err != nil {
		return c.fail(err)
	}
	g := d.Grants
	if v, ok := c.values["grant"]; ok {
		g.Capabilities = listOf(v)
	}
	if v, ok := c.values["library"]; ok {
		g.Libraries, g.AllLibraries = listOf(v), false
	}
	if flags["all-libraries"] {
		g.Libraries, g.AllLibraries = nil, true
	}
	libs := strings.Join(g.Libraries, ", ")
	if g.AllLibraries {
		libs = "all"
	}
	if code := c.confirm(yes, fmt.Sprintf("Grant %s: %s; libraries: %s.", id, strings.Join(g.Capabilities, ", "), libs)); code != 0 {
		return code
	}
	if _, err := c.ops.SetGrants(ctx, id, g, c.actor()); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.out, "%s: grants saved.\n", id)
	return 0
}

func (c *pluginsCLI) policy(ctx context.Context, yes bool) int {
	v, set := c.values["allow-unreviewed"]
	if !set {
		p, err := c.ops.Policy(ctx)
		if err != nil {
			return c.fail(err)
		}
		fmt.Fprintf(c.out, "allow unreviewed plugins: %v\n", p.AllowUnreviewed)
		return 0
	}
	on := false
	switch strings.ToLower(v) {
	case "on", "true", "yes", "1":
		on = true
	case "off", "false", "no", "0":
	default:
		fmt.Fprintln(c.errOut, "plugins: --allow-unreviewed is on or off")
		return exitComponentsFailed
	}
	what := "Stop allowing unreviewed plugins (those installed stay, held until allowed again)."
	if on {
		what = "Allow unreviewed plugins: plugins no store has reviewed may be installed, each with its name typed."
	}
	if code := c.confirm(yes, what); code != 0 {
		return code
	}
	if _, err := c.ops.SetPolicy(ctx, plugins.Policy{AllowUnreviewed: on}, c.actor()); err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.out, "Policy saved.")
	return 0
}

// ---- developer mode (story 54.10 §4) ----

// folderPackage packs an unpacked plugin folder as `mkp pack` does (every file but the
// package's own, hidden files and the build's target folder): the package, its plugin id
// and its English name, and a fingerprint of the files (their paths, sizes and times).
func folderPackage(dir string) (pkg []byte, id, name, fingerprint string, err error) {
	files := map[string][]byte{}
	h := sha256.New()
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(dir, p)
		base := d.Name()
		if rel != "." && (strings.HasPrefix(base, ".") || (d.IsDir() && (base == "target" || base == "node_modules"))) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel = filepath.ToSlash(rel)
		switch rel {
		case componentpack.PluginFilesName, componentpack.PluginDeveloperSig, componentpack.PluginStoreSig:
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		files[rel] = b
		return nil
	})
	if err != nil {
		return nil, "", "", "", err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		fmt.Fprintf(h, "%s %x\n", n, sum)
	}
	var buf bytes.Buffer
	if _, err := componentpack.BuildPlugin(&buf, files, nil); err != nil {
		return nil, "", "", "", err
	}
	id, nameKey := manifestIDAndNameKey(files[componentpack.PluginManifestName])
	var words map[string]any
	_ = json.Unmarshal(files["i18n/en.json"], &words)
	name, _ = words[nameKey].(string)
	return buf.Bytes(), id, name, hex.EncodeToString(h.Sum(nil)), nil
}

// manifestIDAndNameKey reads the manifest's id and its strings.name key (the validator
// has already accepted the TOML when the package was built).
func manifestIDAndNameKey(src []byte) (id, nameKey string) {
	section := ""
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"`)
		switch {
		case section == "" && k == "id":
			id = v
		case section == "strings" && k == "name":
			nameKey = v
		}
	}
	return id, nameKey
}

// dev installs folder as a Development plugin, turned on, and reinstalls it each time a
// file changes (a new package, the same version: the running one drains and the new one
// takes over); it removes the plugin when it stops, unless keep.
func (c *pluginsCLI) dev(ctx context.Context, folder string, keep bool) int {
	p, err := c.ops.Policy(ctx)
	if err != nil {
		return c.fail(err)
	}
	if !p.AllowUnreviewed {
		fmt.Fprintln(c.errOut, "plugins: developer mode needs unreviewed plugins allowed: `maktaba-server plugins policy --allow-unreviewed=on`")
		return exitComponentsFailed
	}
	poll := c.poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	stop := c.stop
	if stop == nil {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sig)
		ch := make(chan struct{})
		go func() { <-sig; close(ch) }()
		stop = ch
	}
	installed, last, id := false, "", ""
	load := func() int {
		pkg, pid, name, fingerprint, err := folderPackage(folder)
		if err != nil {
			fmt.Fprintf(c.errOut, "plugins dev: %v\n", err)
			return exitComponentsFailed
		}
		if fingerprint == last {
			return 0
		}
		last = fingerprint
		expect := ""
		if installed {
			expect = pid
		}
		start := time.Now()
		res, err := c.ops.Install(ctx, plugins.InstallRequest{Package: bytes.NewReader(pkg), ExpectID: expect,
			Enable: true, ConfirmName: name, ReviewLevel: "development"}, c.actor())
		if err != nil {
			code := c.fail(err)
			if !installed {
				return code
			}
			fmt.Fprintln(c.errOut, "plugins dev: the running version stays; fix it and save again")
			return 0
		}
		installed, id = true, res.PluginID
		if res.EnableError != "" {
			fmt.Fprintf(c.errOut, "plugins dev: not turned on: %s\n", res.EnableError)
		}
		fmt.Fprintf(c.out, "%s %s loaded in %s (Development)\n", res.PluginID, res.Version, time.Since(start).Round(time.Millisecond))
		return 0
	}
	if code := load(); code != 0 {
		return code
	}
	fmt.Fprintf(c.out, "Watching %s: every save reloads it. Stop with Ctrl-C.\n", folder)
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			if keep {
				fmt.Fprintf(c.out, "%s stays installed (--keep).\n", id)
				return 0
			}
			if _, err := c.ops.Apply(context.WithoutCancel(ctx), components.Request{Changes: []components.Change{{
				ID: catalog.PluginPrefix + id, Want: catalog.WantAbsent}}}, c.actor()); err != nil {
				return c.fail(err)
			}
			fmt.Fprintf(c.out, "%s removed (developer mode keeps nothing without --keep).\n", id)
			return 0
		case <-t.C:
			_ = load()
		}
	}
}
