package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// bundleRelease is the version label an app-bundle launcher logs.
const bundleRelease = "app"

// ExitRestart (EX_TEMPFAIL) is the server's "restart me": the launcher starts
// it again at once, re-reading current (design §2.2 step 4).
const ExitRestart = 75

// EnvStopFile names a file the launcher creates to ask the server to stop
// gracefully (Windows, where a console-less process has no SIGTERM).
const EnvStopFile = "MAKTABA_STOP_FILE"

// Config tunes the launcher (defaults from design §2.2).
type Config struct {
	Root string
	// Bundle is the app payload directory (app-bundle mode, bundle.go): when
	// set, the launcher starts Bundle/bin/maktaba-server and Root only holds
	// its lock and log (no release pointers, no rollback, no self-update).
	Bundle string
	// Args are passed to `maktaba-server serve` after "serve".
	Args []string
	// StopEvent names a Windows event whose signal ends Run like a stop
	// request (app-bundle mode: StopEventName, stopevent_windows.go). Empty:
	// none; elsewhere it is ignored.
	StopEvent string

	PendingTimeout time.Duration // no confirmation within this → previous (15 min)
	LoopWindow     time.Duration // boot-loop window (10 min)
	LoopMax        int           // non-75 exits of a pending release within LoopWindow (3)
	BackoffInitial time.Duration // 1 s
	BackoffMax     time.Duration // 30 s
	StableAfter    time.Duration // a run this long resets the backoff (60 s)
	StopGrace      time.Duration // stop request → kill after (60 s)
	Poll           time.Duration // pending-timeout check interval (5 s)

	Stdout, Stderr io.Writer
	Now            func() time.Time
}

func (c *Config) defaults() {
	if c.PendingTimeout <= 0 {
		c.PendingTimeout = 15 * time.Minute
	}
	if c.LoopWindow <= 0 {
		c.LoopWindow = 10 * time.Minute
	}
	if c.LoopMax <= 0 {
		c.LoopMax = 3
	}
	if c.BackoffInitial <= 0 {
		c.BackoffInitial = time.Second
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = 30 * time.Second
	}
	if c.StableAfter <= 0 {
		c.StableAfter = time.Minute
	}
	if c.StopGrace <= 0 {
		c.StopGrace = time.Minute
	}
	if c.Poll <= 0 {
		c.Poll = 5 * time.Second
	}
	if c.Stdout == nil {
		c.Stdout = os.Stdout
	}
	if c.Stderr == nil {
		c.Stderr = os.Stderr
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// launcher is one Run.
type launcher struct {
	cfg Config
	l   layout.Layout
	log *eventLog

	// boot-loop guard, per pending version: when this launcher first started
	// it, and its recent non-75 exits.
	guardVersion string
	firstStart   time.Time
	failures     []time.Time
}

// Run starts the current release and keeps it running until ctx ends or
// cfg.StopEvent is set (a stop request), then stops it and returns nil. It
// returns an error only when it cannot run at all (another launcher holds the
// install, no layout).
func Run(ctx context.Context, cfg Config) error {
	cfg.defaults()
	l := layout.New(cfg.Root)
	lk, err := l.Lock()
	if err != nil {
		if errors.Is(err, layout.ErrLocked) {
			fmt.Fprintf(cfg.Stderr, "maktaba-launcher: another launcher runs this install (%s): already_running\n", l.Root)
		}
		return err
	}
	defer layout.Unlock(lk)
	ln := &launcher{cfg: cfg, l: l, log: newEventLog(l.LauncherLog(), cfg.Stderr, cfg.Now)}
	defer ln.log.Close()
	if cfg.Bundle == "" {
		removeOldSelf(l)
	}
	if cfg.StopEvent != "" {
		var release func()
		ctx, release, err = watchStopEvent(ctx, cfg.StopEvent, ln.log)
		if err != nil { // still stoppable by a signal or the agent's job; the server runs on
			ln.log.Event("launcher_stop_event_error", "", map[string]any{"name": cfg.StopEvent, "err": err.Error()})
		}
		defer release()
	}
	return ln.loop(ctx)
}

func (ln *launcher) loop(ctx context.Context) error {
	backoff := ln.cfg.BackoffInitial
	for {
		if ctx.Err() != nil {
			return nil
		}
		if ln.cfg.Bundle == "" { // an app bundle has no pointers to reconcile
			ln.reconcile()
		}

		ver := ln.choose()
		if ver == "" {
			if !sleepCtx(ctx, backoff) {
				return nil
			}
			backoff = minDur(backoff*2, ln.cfg.BackoffMax)
			continue
		}

		res := ln.runOnce(ctx, ver)
		switch {
		case res.stopped:
			ln.log.Event("launcher_stopped", ver, map[string]any{"exit": res.code})
			return nil
		case res.timedOut:
			ln.rollback(ver, "pending_timeout")
			backoff = ln.cfg.BackoffInitial
			continue
		case res.code == ExitRestart:
			ln.log.Event("launcher_restart", ver, map[string]any{"exit": ExitRestart})
			backoff = ln.cfg.BackoffInitial
			continue
		}

		detail := map[string]any{"exit": res.code, "ran_ms": res.ran.Milliseconds()}
		if res.err != nil {
			detail["err"] = res.err.Error()
		}
		ln.log.Event("launcher_child_exit", ver, detail)
		if ln.isPending(ver) {
			now := ln.cfg.Now()
			ln.failures = append(ln.failures, now)
			cut := now.Add(-ln.cfg.LoopWindow)
			kept := ln.failures[:0]
			for _, t := range ln.failures {
				if t.After(cut) {
					kept = append(kept, t)
				}
			}
			ln.failures = kept
			if len(ln.failures) >= ln.cfg.LoopMax {
				if ln.rollback(ver, "boot_loop") {
					backoff = ln.cfg.BackoffInitial
					continue
				}
			}
		}
		if res.ran >= ln.cfg.StableAfter {
			backoff = ln.cfg.BackoffInitial
		}
		if !sleepCtx(ctx, backoff) {
			return nil
		}
		backoff = minDur(backoff*2, ln.cfg.BackoffMax)
	}
}

func (ln *launcher) reconcile() {
	if changed, err := ln.l.Reconcile(); err != nil {
		ln.log.Event("launcher_reconcile_error", "", map[string]any{"err": err.Error()})
	} else if changed {
		ln.log.Event("launcher_reconciled", "", map[string]any{"detail": "dropped a pending marker that does not match current"})
	}
}

// choose picks the release to start: current when it validates, else
// previous (story 35.2 AC3, EC1, EC2). "" means nothing can be started.
func (ln *launcher) choose() string {
	if ln.cfg.Bundle != "" {
		return bundleRelease
	}
	cur, err := ln.l.Current()
	if err == nil {
		verr := ln.l.ValidateRelease(cur)
		if verr == nil {
			return cur
		}
		ln.log.Event("launcher_refused", cur, map[string]any{"reason": verr.Error()})
		if ln.isPending(cur) { // a pending release that cannot even be started: the guard's case
			if ln.rollback(cur, "refused") {
				return ln.choose()
			}
			return ""
		}
	} else {
		ln.log.Event("launcher_refused", "", map[string]any{"reason": "current: " + err.Error()})
	}
	prev, perr := ln.l.Previous()
	if perr != nil || prev == cur {
		ln.log.Event("no_previous", cur, nil)
		return ""
	}
	if verr := ln.l.ValidateRelease(prev); verr != nil {
		ln.log.Event("launcher_refused", prev, map[string]any{"reason": verr.Error()})
		ln.log.Event("no_previous", cur, nil)
		return ""
	}
	return prev
}

func (ln *launcher) isPending(ver string) bool {
	if ln.cfg.Bundle != "" {
		return false
	}
	p, err := ln.l.ReadPending()
	return err == nil && p != nil && p.Version == ver
}

// rollback is the boot-loop guard firing: current = previous, pending
// removed, launcher_rollback logged (design §2.2 step 5). false when there
// is no previous to go back to (EC1: keep retrying, log no_previous).
func (ln *launcher) rollback(ver, reason string) bool {
	to, err := ln.l.Rollback()
	ln.failures = nil
	ln.guardVersion = ""
	if err != nil {
		ln.log.Event("no_previous", ver, map[string]any{"reason": reason, "err": err.Error()})
		return false
	}
	ln.log.Event("launcher_rollback", ver, map[string]any{"reason": reason, "to": to})
	return true
}

type runResult struct {
	code     int
	err      error
	ran      time.Duration
	stopped  bool // ctx ended: a stop request
	timedOut bool // pending_timeout fired
}

// runOnce starts bin/releases/<ver>/maktaba-server serve and waits for it.
func (ln *launcher) runOnce(ctx context.Context, ver string) runResult {
	pending := ln.isPending(ver)
	if pending && ln.guardVersion != ver {
		ln.guardVersion, ln.firstStart, ln.failures = ver, ln.cfg.Now(), nil
	}
	if !pending {
		ln.guardVersion, ln.failures = "", nil
	}

	_ = ln.l.TakeRestartRequest() // a stale request must not restart the release we start now
	stopFile := filepath.Join(ln.l.UpdatesDir(), "stop-"+randHex())
	_ = os.Remove(stopFile)
	defer func() { _ = os.Remove(stopFile) }()

	server, env := ln.l.ServerPath(ver), []string{
		layout.EnvLauncher + "=1",
		layout.EnvReleaseDir + "=" + ln.l.ReleaseDir(ver),
		layout.EnvInstallRoot + "=" + ln.l.Root,
		EnvStopFile + "=" + stopFile,
	}
	if ln.cfg.Bundle != "" {
		// Not MAKTABA_LAUNCHER/RELEASE_DIR/INSTALL_ROOT: the server must not
		// treat the read-only payload as a release layout it can confirm,
		// stage into or roll back.
		server, env = filepath.Join(ln.cfg.Bundle, "bin", layout.Exe("maktaba-server")), []string{
			EnvAppBundle + "=" + ln.cfg.Bundle,
			envUpdateDisable + "=1",
			EnvStopFile + "=" + stopFile,
		}
	}
	cmd := exec.Command(server, append([]string{"serve"}, ln.cfg.Args...)...)
	cmd.Dir = ln.l.Root
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = ln.cfg.Stdout, ln.cfg.Stderr
	prepareChild(cmd)
	start := ln.cfg.Now()
	if err := cmd.Start(); err != nil {
		ln.log.Event("launcher_start_error", ver, map[string]any{"err": err.Error()})
		return runResult{code: -1, err: err}
	}
	child := attachChild(cmd)
	defer child.release()
	ln.log.Event("launcher_start", ver, map[string]any{"pid": cmd.Process.Pid, "pending": pending})

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	tick := time.NewTicker(ln.cfg.Poll)
	defer tick.Stop()

	finish := func(err error, r runResult) runResult {
		r.ran = ln.cfg.Now().Sub(start)
		r.code = exitCode(cmd, err)
		if err != nil && r.code == -1 {
			r.err = err
		}
		return r
	}
	for {
		select {
		case err := <-done:
			return finish(err, runResult{})
		case <-ctx.Done():
			return finish(ln.stop(cmd, child, done, stopFile), runResult{stopped: true})
		case <-tick.C:
			if ln.guardVersion == ver && ln.isPending(ver) &&
				ln.cfg.Now().Sub(ln.firstStart) >= ln.cfg.PendingTimeout {
				ln.log.Event("launcher_pending_timeout", ver, map[string]any{"after_s": int(ln.cfg.PendingTimeout.Seconds())})
				return finish(ln.stop(cmd, child, done, stopFile), runResult{timedOut: true})
			}
		}
	}
}

// stop asks the server to stop (SIGTERM; Windows: CTRL_BREAK and the stop
// file), then kills it after StopGrace. It returns cmd.Wait's error.
func (ln *launcher) stop(cmd *exec.Cmd, child *childHandle, done <-chan error, stopFile string) error {
	_ = os.WriteFile(stopFile, []byte("stop\n"), 0o644)
	if err := askStop(cmd, child); err != nil {
		ln.log.Event("launcher_stop_signal_error", "", map[string]any{"err": err.Error()})
	}
	select {
	case err := <-done:
		return err
	case <-time.After(ln.cfg.StopGrace):
		ln.log.Event("launcher_kill", "", map[string]any{"after_s": int(ln.cfg.StopGrace.Seconds())})
		child.kill()
		_ = cmd.Process.Kill()
		return <-done
	}
}

func exitCode(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func randHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
