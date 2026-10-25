package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
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
// arguments it runs that command of this payload's maktaba-server instead, after the
// same bootstrap (`docker compose run --rm maktaba setup`).

// containerSetupEvery is how often a container that is not set up looks for server.toml.
var containerSetupEvery = 5 * time.Second

// containerDeps are what the container command uses (tests replace them).
type containerDeps struct {
	rt      appruntime.Runtime
	payload string // the image's payload this binary is in ("" none)
	self    string // this binary
	config  string // server.toml
	logFile string // the launcher's --log-file
	out     io.Writer
	every   time.Duration
	// exec replaces this process with path (syscall.Exec).
	exec func(path string, argv, env []string) error
	// start runs the launcher until it exits or ctx ends (a stop: passed on), and returns
	// its exit code.
	start func(ctx context.Context, path string, args, env []string) (int, error)
	// follow copies what is appended to the log to out until ctx ends.
	follow func(ctx context.Context, path string, out io.Writer)
}

func newContainerDeps() containerDeps {
	self, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(self); err == nil {
		self = r
	}
	return containerDeps{
		rt:      appruntime.New(filepath.Join(config.LocalDirs().Data, "runtime")),
		payload: payloadOfThisBinary(),
		self:    self,
		config:  config.Path(),
		logFile: filepath.Join(config.LogsDir(), "server.log"),
		out:     os.Stdout,
		every:   containerSetupEvery,
		exec:    execProcess,
		start: func(ctx context.Context, path string, args, env []string) (int, error) {
			return runLauncher(ctx, path, args, env, os.Stdout)
		},
		follow: func(ctx context.Context, path string, out io.Writer) {
			followLog(ctx, path, out, 250*time.Millisecond)
		},
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
// makes the machine its owner (headless: the server updates it from its releases).
func (c containerDeps) bootstrap(ctx context.Context) error {
	if c.payload == "" {
		return errors.New("this maktaba-server is not in a payload: the image runs /opt/maktaba/server/bin/maktaba-server")
	}
	st, err := c.rt.Status(c.payload)
	if err != nil {
		return err
	}
	switch plan := planContainer(st); plan {
	case containerFirst, containerNewer:
		c.logf("putting Maktaba %s in %s", st.FromVersion, c.rt.Root)
		res, err := c.rt.Install(ctx, c.payload, nil, appruntime.Options{})
		if err != nil {
			return err
		}
		if plan == containerNewer && !res.First {
			// nothing runs yet: `current` moves without a service to stop
			if _, err := c.rt.Activate(ctx, filepath.Base(res.Dir), nil, appruntime.Options{}); err != nil {
				return err
			}
			c.logf("Maktaba %s replaces %s (this image is newer)", st.FromVersion, orNone(st.Version))
			if pruned, err := c.rt.Prune(ctx, 2); err == nil && len(pruned) > 0 {
				c.logf("removed the older runtimes %s", strings.Join(pruned, ", "))
			}
		}
	case containerOlder:
		c.logf("this image's Maktaba %s is older than %s, the newest this volume ran: the volume's %s runs",
			st.FromVersion, st.Watermark, orNone(st.Version))
	}
	if c.rt.ReadOwner() != appruntime.OwnerHeadless {
		return c.rt.SetOwner(appruntime.OwnerHeadless)
	}
	return nil
}

// waitSetup waits for server.toml (setup writes it last), saying once how to set up; false
// when ctx ended first (the container stopped).
func (c containerDeps) waitSetup(ctx context.Context) bool {
	said := false
	for {
		if _, err := os.Stat(c.config); err == nil {
			return true
		}
		if !said {
			c.logf("Maktaba is not set up yet: run its setup in this image, `docker compose run --rm maktaba setup`; " +
				"the server starts here by itself once it is done")
			said = true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(c.every):
		}
	}
}

// run is the command: the bootstrap, then the given command (replacing this process) or,
// with none, the launcher once set up; it returns the launcher's exit code.
func (c containerDeps) run(ctx context.Context, args []string) (int, error) {
	if err := c.bootstrap(ctx); err != nil {
		return 1, err
	}
	env := containerEnv(os.Environ())
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
	code, err := c.start(ctx, launcher, []string{"--log-file", c.logFile, "serve"}, env)
	stopFollow()
	<-followed
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
