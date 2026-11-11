package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/mediaaccess"
)

// container: the Docker install's entrypoint (deploy/docker/server/Dockerfile, behind
// tini). The image is only a bootstrap: it carries its release's Linux payload, which
// this command puts on the volume's runtime (<data>/runtime) when the runtime has none or
// an older one (never an older payload over a newer runtime: the volume's database may be
// migrated past it), owned by the machine itself (headless). Once setup wrote server.toml
// it runs the runtime's launcher, as a headless install's systemd unit does, copies the
// launcher's log to its own output (docker logs) and passes a stop on. From there the
// server updates the runtime on the volume by itself, exactly as a headless Linux install
// (updater.RuntimeInstaller: the release's payload verified in process, a new runtime,
// `current` moved under the launcher's guard, exit 75 and the launcher starts it). With
// arguments it runs that command of this payload's maktaba-server instead, after a
// bootstrap that only fills an empty volume (`docker compose run --rm maktaba setup`).
//
// A newer image pulled later is switched to as an update is: under the launcher's guard
// (pending.json, run "docker-image"), the watermark left where it was. This command then
// keeps it once its server has answered with the new version for containerSettle (an
// update's commit: guard cleared, watermark raised, older runtimes pruned); when the
// launcher goes back instead (the new release crash-loops, or never answers by the guard's
// deadline), the image's version is recorded as refused and not switched to again (a newer
// image is), and the image that ran before still runs, as the watermark never rose.
//
// Before all of it the preflight runs (container_preflight.go, story 59.01): the image runs
// as whatever uid:gid it is started with, refuses root, a privileged container and the
// Docker socket, checks that the volume takes a write, and names a uid /etc/passwd lacks
// (nss_wrapper); a refusal exits 78 with its message in English and Arabic.

// containerSetupEvery is how often a container that is not set up looks for server.toml.
var containerSetupEvery = 5 * time.Second

// containerSettle is how long the server of a newer image must answer with its version
// before the switch is kept; containerConfirmEvery how often that is asked; and
// containerGuardFor how long the launcher's guard lets it take to come up (the database
// step's dump and migrations included) before it goes back.
var (
	containerSettle       = 3 * time.Minute
	containerConfirmEvery = 10 * time.Second
	containerGuardFor     = 2 * time.Hour
)

// imageRunID is the run the guard of a newer image's switch names.
const imageRunID = "docker-image"

// imageRefusedFile records, in the launcher root's updates directory, the image version
// whose switch the launcher went back from: not tried again.
const imageRefusedFile = "image-refused.json"

// containerDeps are what the container command uses (tests replace them).
type containerDeps struct {
	rt      appruntime.Runtime
	payload string // the image's payload this binary is in ("" none)
	self    string // this binary
	config  string // server.toml
	logFile string // the launcher's --log-file
	out     io.Writer
	every   time.Duration
	// launcherRoot is the runtime launcher's state root (its guard, pending.json):
	// <data>/maktaba, the runtime's parent.
	launcherRoot string
	// record is the home's database record the launcher's fallback marks ("" none).
	record func() string
	// version is the release the server answers with now ("" none).
	version func(ctx context.Context) string
	now     func() time.Time
	// settle, confirmEvery and guardFor are containerSettle, containerConfirmEvery and
	// containerGuardFor.
	settle, confirmEvery, guardFor time.Duration
	// exec replaces this process with path (syscall.Exec).
	exec func(path string, argv, env []string) error
	// start runs the launcher until it exits or ctx ends (a stop: passed on), and returns
	// its exit code.
	start func(ctx context.Context, path string, args, env []string) (int, error)
	// follow copies what is appended to the log to out until ctx ends.
	follow func(ctx context.Context, path string, out io.Writer)
	// platform is the NAS the container says it runs on (MAKTABA_PLATFORM; "" none): the
	// setup hint names that NAS's way to run a command (story 59.10).
	platform string
	// preflight runs before anything else (story 59.01): the variables every child gets,
	// or a refusal (exit 78). nil: none (tests).
	preflight func() ([]string, error)
}

func newContainerDeps() containerDeps {
	self, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(self); err == nil {
		self = r
	}
	data := config.LocalDirs().Data
	cfgPath := config.Path()
	return containerDeps{
		rt:           appruntime.New(filepath.Join(data, "runtime")),
		payload:      payloadOfThisBinary(),
		self:         self,
		config:       cfgPath,
		logFile:      filepath.Join(config.LogsDir(), "server.log"),
		out:          os.Stdout,
		every:        containerSetupEvery,
		launcherRoot: data,
		record:       func() string { return databaseRecord(cfgPath) },
		version:      func(ctx context.Context) string { return serverVersion(ctx, cfgPath) },
		now:          time.Now,
		settle:       containerSettle,
		confirmEvery: containerConfirmEvery,
		guardFor:     containerGuardFor,
		exec:         execProcess,
		start: func(ctx context.Context, path string, args, env []string) (int, error) {
			return runLauncher(ctx, path, args, env, os.Stdout)
		},
		follow: func(ctx context.Context, path string, out io.Writer) {
			followLog(ctx, path, out, 250*time.Millisecond)
		},
		preflight: newPreflight().run,
		platform:  mediaaccess.PlatformFromEnv(),
	}
}

func newContainerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "container [command...]",
		Short: "The Docker image's entrypoint: the volume's runtime, then the server",
		Long: "container puts this image's payload on the volume's runtime when the runtime has none or an older one,\n" +
			"then runs the runtime's launcher once setup wrote server.toml (until then it waits, saying how to set up).\n" +
			"The server then updates the runtime on the volume by itself. With a command it runs that command of this\n" +
			"payload's maktaba-server after the same bootstrap: `docker compose run --rm maktaba setup`.",
		Hidden:             true,
		DisableFlagParsing: true, // every argument is the command's
		RunE: func(_ *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			code, err := newContainerDeps().run(ctx, args)
			if err != nil {
				return err
			}
			if code != 0 {
				os.Exit(code) // the launcher's: the restart policy decides what comes next
			}
			return nil
		},
	}
}

// containerPlan is what a container's start does with its image's payload.
type containerPlan string

const (
	containerFirst containerPlan = "first" // no runtime yet: the payload becomes its first
	containerNewer containerPlan = "newer" // newer than the runtime's current: installed and switched to
	containerOlder containerPlan = "older" // older than the newest the volume ran: the volume's runtime runs
	containerRuns  containerPlan = "runs"  // the runtime runs this payload already, or a newer one
)

// planContainer decides from the runtime's status against the image's payload.
func planContainer(st appruntime.Status) containerPlan {
	switch {
	case st.FromVersion == "":
		return containerRuns // no payload to compare (not the image's binary)
	case st.Current == "":
		return containerFirst
	case st.Downgrade:
		return containerOlder
	case st.UpdatePending:
		return containerNewer
	}
	return containerRuns
}

func (c containerDeps) logf(format string, args ...any) {
	fmt.Fprintf(c.out, "[container] "+format+"\n", args...)
}

// bootstrap gives the volume's runtime the image's payload as planContainer says, and
// makes the machine its owner (headless: the server updates it from its releases). A newer
// image is switched to under the launcher's guard, never before a command (command: a
// `docker compose run` beside a container that may be running) nor when its version was
// refused before. It returns the guard of a switch that waits to be kept (one it wrote, or
// one an earlier start of this container left), nil when there is none.
func (c containerDeps) bootstrap(ctx context.Context, command bool) (*layout.Guard, error) {
	if c.payload == "" {
		return nil, errors.New("this maktaba-server is not in a payload: the image runs /opt/maktaba/server/bin/maktaba-server")
	}
	st, err := c.rt.Status(c.payload)
	if err != nil {
		return nil, err
	}
	var pending *layout.Guard
	switch plan := planContainer(st); {
	case plan == containerFirst:
		c.logf("putting Maktaba %s in %s", st.FromVersion, c.rt.Root)
		if _, err := c.rt.Install(ctx, c.payload, nil, appruntime.Options{}); err != nil {
			return nil, err
		}
	case plan == containerNewer && c.refused() == layout.NormalizeVersion(st.FromVersion):
		c.logf("this image's Maktaba %s could not start here before: the volume's %s runs (a newer image is tried)",
			st.FromVersion, orNone(st.Version))
	case plan == containerNewer && command:
		c.logf("this image's Maktaba %s is newer than the volume's %s: it goes on the volume when the container starts "+
			"from this image", st.FromVersion, orNone(st.Version))
	case plan == containerNewer:
		if pending, err = c.switchNewer(ctx, st); err != nil {
			return nil, err
		}
	case plan == containerOlder:
		c.logf("this image's Maktaba %s is older than %s, the newest this volume ran: the volume's %s runs",
			st.FromVersion, st.Watermark, orNone(st.Version))
	}
	if c.rt.ReadOwner() != appruntime.OwnerHeadless {
		if err := c.rt.SetOwner(appruntime.OwnerHeadless); err != nil {
			return nil, err
		}
	}
	if pending == nil && !command {
		// a container restarted before it kept a newer image's switch: it watches on
		if g, err := layout.ReadGuard(c.launcherRoot); err == nil && g != nil && g.RunID == imageRunID {
			pending = g
		}
	}
	return pending, nil
}

// switchNewer installs the newer image's payload and points `current` at it as an
// update's switch does (updater.RuntimeInstaller.Switch): the launcher's guard first, the
// runtime that ran as the one to go back to, the watermark left where it was.
func (c containerDeps) switchNewer(ctx context.Context, st appruntime.Status) (*layout.Guard, error) {
	c.logf("putting Maktaba %s in %s", st.FromVersion, c.rt.Root)
	res, err := c.rt.Install(ctx, c.payload, nil, appruntime.Options{})
	if err != nil {
		return nil, err
	}
	now := c.now().UTC()
	g := layout.Guard{Version: layout.NormalizeVersion(st.FromVersion), RunID: imageRunID, Since: now,
		Deadline: now.Add(c.guardFor), PreviousRuntime: st.Current}
	if c.record != nil {
		g.Record = c.record()
	}
	if err := layout.WriteGuard(c.launcherRoot, g); err != nil {
		return nil, err
	}
	if _, err := c.rt.Point(ctx, filepath.Base(res.Dir), appruntime.Options{}); err != nil {
		_ = layout.ClearGuard(c.launcherRoot)
		return nil, err
	}
	c.logf("Maktaba %s replaces %s (this image is newer), under the launcher's guard: it goes back to %s if %s "+
		"cannot start", st.FromVersion, orNone(st.Version), orNone(st.Version), st.FromVersion)
	return &g, nil
}

// confirm keeps a newer image's switch once its server has answered with the new version
// for c.settle, as an update's commit does: the guard cleared, the watermark raised, the
// runtimes but this one and the one before pruned. When the guard is gone with `current`
// back on the runtime before (the launcher went back), the image's version is recorded as
// refused. It ends with ctx (the launcher ended: the next start watches on).
func (c containerDeps) confirm(ctx context.Context, g layout.Guard) {
	var since time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.confirmEvery):
		}
		// the guard before `current`: the launcher points `current` back, then clears it
		cur, err := layout.ReadGuard(c.launcherRoot)
		if err != nil {
			continue
		}
		if cur == nil || cur.RunID != g.RunID || cur.Version != g.Version {
			if dir, _ := c.rt.Current(); g.PreviousRuntime != "" && dir == g.PreviousRuntime {
				c.refuse(g.Version)
				c.logf("Maktaba %s could not start here: the launcher went back to %s; this image's release is not "+
					"tried again (a newer image is)", g.Version, dir)
			}
			return
		}
		if layout.NormalizeVersion(c.version(ctx)) != g.Version {
			since = time.Time{}
			continue
		}
		if since.IsZero() {
			since = c.now()
		}
		if c.now().Sub(since) < c.settle {
			continue
		}
		if err := layout.ClearGuard(c.launcherRoot); err != nil {
			c.logf("could not keep Maktaba %s: %v", g.Version, err)
			continue
		}
		_ = c.rt.RaiseWatermark(g.Version)
		_ = os.Remove(filepath.Join(c.launcherRoot, "updates", imageRefusedFile))
		if pruned, err := c.rt.Prune(ctx, 2); err == nil && len(pruned) > 0 {
			c.logf("removed the older runtimes %s", strings.Join(pruned, ", "))
		}
		c.logf("Maktaba %s runs: kept", g.Version)
		return
	}
}

// refused is the image version recorded as refused ("" none).
func (c containerDeps) refused() string {
	b, err := os.ReadFile(filepath.Join(c.launcherRoot, "updates", imageRefusedFile))
	if err != nil {
		return ""
	}
	var r struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &r) != nil {
		return ""
	}
	return layout.NormalizeVersion(r.Version)
}

// refuse records version as refused.
func (c containerDeps) refuse(version string) {
	b, _ := json.Marshal(map[string]any{"version": version, "at": c.now().UTC()})
	_ = os.MkdirAll(filepath.Join(c.launcherRoot, "updates"), 0o700)
	_ = layout.WriteFileAtomic(filepath.Join(c.launcherRoot, "updates", imageRefusedFile), append(b, '\n'), 0o600)
}

// databaseRecord is the home's database record the launcher's fallback marks for the dump
// to be put back ("" when there is none: not set up, or a machine without one).
func databaseRecord(cfgPath string) string {
	cfg, found, err := config.LoadFrom(cfgPath)
	if err != nil || !found {
		return ""
	}
	p := apppayload.DatabasePath(cfg.StateDir())
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// serverVersion is the release this machine's API answers with (/api/system/version on
// its own port, server.toml's listen), "" when it does not answer.
func serverVersion(ctx context.Context, cfgPath string) string {
	cfg, found, err := config.LoadFrom(cfgPath)
	if err != nil || !found {
		return ""
	}
	_, port, err := net.SplitHostPort(strings.TrimSpace(cfg.Server.Listen))
	if err != nil || port == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/api/system/version", nil)
	if err != nil {
		return ""
	}
	withLocalToken(req, cfg)
	resp, err := noRedirect(http.DefaultClient).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&v) != nil {
		return ""
	}
	return v.Version
}

// waitSetup waits for a finished setup, saying once how to set up; false when ctx ended
// first (the container stopped). server.toml alone is not that: setup's finish step writes
// it before it creates the database, and a server started then runs its own initdb on the
// same folder as setup's (the TrueNAS app's CI saw both fail, Epic 59 story 59.08). So setup's
// pending file (state/setup.json, removed once the finish step is done) must be gone too.
func (c containerDeps) waitSetup(ctx context.Context) bool {
	said := false
	for {
		if _, err := os.Stat(c.config); err == nil && !setupRunning(c.config) {
			return true
		}
		if !said {
			c.logf("%s", setupHint(c.platform))
			said = true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(c.every):
		}
	}
}

// setupRunning is whether a setup of the install at cfgPath has not finished (its pending
// file is there). A config that does not load is not this check's to judge: the server says.
func setupRunning(cfgPath string) bool {
	cfg, _, err := config.LoadFrom(cfgPath)
	if err != nil {
		return false
	}
	_, err = os.Stat(setupPendingFor(cfg))
	return err == nil
}

// setupHint is what a container not set up says once: how to run its setup on platform
// (MAKTABA_PLATFORM, which the TrueNAS app sets; its households have no compose file to run).
func setupHint(platform string) string {
	if strings.EqualFold(strings.TrimSpace(platform), mediaaccess.PlatformTrueNAS) {
		return "Maktaba is not set up yet: open this app's Shell in TrueNAS (Apps → Installed → Maktaba → " +
			"Workloads → maktaba → Shell) and run `/opt/maktaba/server/bin/maktaba-server container setup`; " +
			"the server starts here by itself once it is done"
	}
	return "Maktaba is not set up yet: run its setup in this image, `docker compose run --rm maktaba setup`; " +
		"the server starts here by itself once it is done"
}

// run is the command: the bootstrap, then the given command (replacing this process) or,
// with none, the launcher once set up; it returns the launcher's exit code.
func (c containerDeps) run(ctx context.Context, args []string) (int, error) {
	var extra []string
	if c.preflight != nil {
		var err error
		if extra, err = c.preflight(); err != nil {
			var r *refusal
			if errors.As(err, &r) {
				fmt.Fprintln(c.out, r.message)
				return exitConfig, nil
			}
			return 1, err
		}
		if len(extra) > 0 {
			c.logf("user %d has no name in the image: nss_wrapper names it %s (%s)", os.Getuid(), nssName, nssDir)
		}
	}
	pending, err := c.bootstrap(ctx, len(args) > 0)
	if err != nil {
		return 1, err
	}
	env := withEnv(containerEnv(os.Environ()), extra...)
	if len(args) > 0 {
		return 0, c.exec(c.self, append([]string{c.self}, args...), env)
	}
	if !c.waitSetup(ctx) {
		return 0, nil
	}
	launcher := filepath.Join(c.rt.Root, appruntime.CurrentLink, "bin", "maktaba-launcher")
	c.logf("starting %s", launcher)
	fctx, stopFollow := context.WithCancel(context.Background())
	followed := make(chan struct{})
	go func() {
		defer close(followed)
		c.follow(fctx, c.logFile, c.out)
	}()
	confirmed := make(chan struct{})
	go func() {
		defer close(confirmed)
		if pending != nil {
			c.confirm(fctx, *pending)
		}
	}()
	code, err := c.start(ctx, launcher, []string{"--log-file", c.logFile, "serve"}, env)
	stopFollow()
	<-followed
	<-confirmed
	return code, err
}

// containerEnv is env with MAKTABA_CONTAINER set (the image sets it; a run of this
// command elsewhere is taken as the Docker install's too).
func containerEnv(env []string) []string {
	for _, kv := range env {
		if strings.HasPrefix(kv, config.EnvContainer+"=") && strings.TrimSpace(strings.TrimPrefix(kv, config.EnvContainer+"=")) != "" {
			return env
		}
	}
	return append(env, config.EnvContainer+"="+config.ContainerDocker)
}

// runLauncher runs the launcher until it exits, or until ctx ends: then it asks it to stop
// (SIGTERM: the launcher drains the supervisor, Postgres last) and waits for it.
func runLauncher(ctx context.Context, path string, args, env []string, out io.Writer) (int, error) {
	cmd := exec.Command(path, args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, out, out
	if err := cmd.Start(); err != nil {
		return 1, err
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		return exitCodeOf(err)
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		code, _ := exitCodeOf(<-waited)
		if code < 0 {
			code = 0 // ended by the stop it was asked for
		}
		return code, nil
	}
}

// exitCodeOf is a Wait's exit code (-1: ended by a signal).
func exitCodeOf(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 1, err
}

// followLog copies what is appended to the log at path to out until ctx ends (and then
// what is left): the launcher writes its own and the server's output there (the logs
// page reads it), and docker logs show this process's output. What the log held before
// is not repeated; a log that appears later is copied from its start, and so is one the
// launcher renamed aside (path.1) or cut, once what was left of the old one is copied.
func followLog(ctx context.Context, path string, out io.Writer, every time.Duration) {
	var f *os.File
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()
	open := func(atEnd bool) {
		g, err := os.Open(path)
		if err != nil {
			return
		}
		if atEnd {
			if _, err := g.Seek(0, io.SeekEnd); err != nil {
				_ = g.Close()
				return
			}
		}
		f = g
	}
	open(true)
	for {
		if f == nil {
			open(false)
		}
		if f != nil {
			_, _ = io.Copy(out, f)
			pos, _ := f.Seek(0, io.SeekCurrent)
			cur, err1 := f.Stat()
			now, err2 := os.Stat(path)
			switch {
			case err2 != nil || (err1 == nil && !os.SameFile(cur, now)):
				// renamed aside (a new one there, or not yet): what the old one got since is
				// copied, then the new one from its start
				_, _ = io.Copy(out, f)
				_ = f.Close()
				f = nil
				open(false)
			case now.Size() < pos:
				_, _ = f.Seek(0, io.SeekStart)
			}
		}
		select {
		case <-ctx.Done():
			if f != nil {
				_, _ = io.Copy(out, f)
			}
			return
		case <-time.After(every):
		}
	}
}
