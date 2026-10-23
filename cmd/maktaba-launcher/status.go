package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Status is what --status prints (story 35.2 AC6).
type Status struct {
	Root             string          `json:"root"`
	Current          string          `json:"current"`
	Previous         string          `json:"previous"`
	Pending          *layout.Pending `json:"pending,omitempty"`
	Watermark        string          `json:"watermark"`
	Releases         []string        `json:"releases"`
	LastRollback     *Entry          `json:"last_rollback,omitempty"`
	LauncherProtocol int             `json:"launcher_protocol"`
	LauncherVersion  string          `json:"launcher_version"`
}

func collectStatus(l layout.Layout) Status {
	s := Status{Root: l.Root, LauncherProtocol: LauncherProtocol, LauncherVersion: Version}
	s.Current, _ = l.Current()
	s.Previous, _ = l.Previous()
	s.Pending, _ = l.ReadPending()
	s.Watermark = l.Watermark()
	s.Releases, _ = l.Versions()
	ents := readLog(l.LauncherLog())
	for i := len(ents) - 1; i >= 0; i-- {
		if ents[i].Event == "launcher_rollback" {
			e := ents[i]
			s.LastRollback = &e
			break
		}
	}
	return s
}

func printStatus(w io.Writer, l layout.Layout, asJSON bool) int {
	s := collectStatus(l)
	if asJSON {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(w, string(b))
		return exitOK
	}
	dash := func(v string) string {
		if v == "" {
			return "-"
		}
		return v
	}
	fmt.Fprintf(w, "root:      %s\n", s.Root)
	fmt.Fprintf(w, "current:   %s\n", dash(s.Current))
	fmt.Fprintf(w, "previous:  %s\n", dash(s.Previous))
	if s.Pending != nil {
		fmt.Fprintf(w, "pending:   %s (since %s, run %s)\n", s.Pending.Version, s.Pending.Since.Format("2006-01-02 15:04:05Z07:00"), dash(s.Pending.RunID))
	} else {
		fmt.Fprintf(w, "pending:   -\n")
	}
	fmt.Fprintf(w, "watermark: %s\n", dash(s.Watermark))
	fmt.Fprintf(w, "releases:  %v\n", s.Releases)
	if s.LastRollback != nil {
		fmt.Fprintf(w, "last rollback: %s from %s (%v)\n", s.LastRollback.TS.Format("2006-01-02 15:04:05Z07:00"),
			s.LastRollback.Version, s.LastRollback.Detail["reason"])
	} else {
		fmt.Fprintf(w, "last rollback: -\n")
	}
	fmt.Fprintf(w, "launcher:  %s (protocol %d)\n", s.LauncherVersion, s.LauncherProtocol)
	return exitOK
}

// ReplaceSelf installs the running executable at target (design §8.3): the
// installed launcher is renamed to target.old (allowed for a running .exe on
// Windows) and this file is renamed into its place. Asking the service manager
// for the restart is the caller's job (story 35.7); target.old is removed at
// the next good start (removeOldSelf: a server started that stayed up).
func ReplaceSelf(target string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if filepath.Clean(self) == filepath.Clean(abs) {
		return fmt.Errorf("%s is already this executable", abs)
	}
	old := abs + ".old"
	_ = os.Remove(old)
	if _, err := os.Stat(abs); err == nil {
		if err := os.Rename(abs, old); err != nil {
			return fmt.Errorf("move the installed launcher aside: %w", err)
		}
	}
	if err := os.Rename(self, abs); err != nil {
		_ = os.Rename(old, abs) // put the old one back
		return fmt.Errorf("move into place: %w", err)
	}
	return nil
}

// removeOldSelf deletes <install>/maktaba-launcher.old left by ReplaceSelf.
func removeOldSelf(l layout.Layout) {
	_ = os.Remove(l.LauncherPath() + ".old")
	if self, err := os.Executable(); err == nil {
		_ = os.Remove(self + ".old")
	}
}

// BundleStatus is --status in app-bundle mode: no releases to list. Paths
// are the platform's (backslashes and .exe names on Windows).
type BundleStatus struct {
	AppBundle string `json:"app_bundle"`
	Server    string `json:"server"` // the maktaba-server the launcher starts
	// Running is the version of the payload it starts; Kept the kept copy it starts in
	// the app's place after a rolled-back update ("" none)
	Running          string        `json:"running,omitempty"`
	Kept             string        `json:"kept,omitempty"`
	Pending          *layout.Guard `json:"pending,omitempty"` // an update being tried, under the guard
	Root             string        `json:"root"`
	LauncherLog      string        `json:"launcher_log"`
	StopEvent        string        `json:"stop_event,omitempty"` // Windows: the event that stops it
	Updates          string        `json:"updates"`
	LauncherVersion  string        `json:"launcher_version"`
	LauncherProtocol int           `json:"launcher_protocol"`
}

func printBundleStatus(w io.Writer, bundle, runtimeRoot, root string, asJSON bool) int {
	run := bundle
	if o := layout.ReadOverride(root); o != "" && runtimeRoot == "" {
		g, _ := layout.ReadGuard(root)
		if g == nil || payloadVersion(bundle) != layout.NormalizeVersion(g.Version) {
			run = o
		}
	}
	s := BundleStatus{
		AppBundle:   bundle,
		Server:      filepath.Join(run, "bin", layout.Exe("maktaba-server")),
		Running:     payloadVersion(run),
		Root:        root,
		LauncherLog: layout.New(root).LauncherLog(),
		StopEvent:   stopEventName,
		// "with the app" / "by this machine" first: the app checks (tools/release) read them
		Updates:          "with the app (the cluster's update: staged here, installed when someone clicks Install in the app)",
		LauncherVersion:  Version,
		LauncherProtocol: LauncherProtocol,
	}
	if run != bundle {
		s.Kept = run
	}
	if g, err := layout.ReadGuard(root); err == nil && g != nil {
		s.Pending = g
	}
	if runtimeRoot != "" && runtimeOwner(runtimeRoot) == runtimeOwnerHeadless {
		s.Updates = "by this machine (the cluster's update, by itself in the window)"
	}
	if asJSON {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(w, string(b))
		return exitOK
	}
	fmt.Fprintf(w, "app bundle: %s\n", s.AppBundle)
	fmt.Fprintf(w, "server:     %s (%s)\n", s.Server, orDash(s.Running))
	if s.Kept != "" {
		fmt.Fprintf(w, "kept copy:  %s (an update was rolled back)\n", s.Kept)
	}
	if s.Pending != nil {
		fmt.Fprintf(w, "pending:    %s (until %s)\n", s.Pending.Version, s.Pending.Deadline.Format(time.RFC3339))
	}
	fmt.Fprintf(w, "state:      %s\n", s.Root)
	fmt.Fprintf(w, "log:        %s\n", s.LauncherLog)
	if s.StopEvent != "" {
		fmt.Fprintf(w, "stop event: %s\n", s.StopEvent)
	}
	fmt.Fprintf(w, "updates:    %s\n", s.Updates)
	fmt.Fprintf(w, "launcher:   %s (protocol %d)\n", s.LauncherVersion, s.LauncherProtocol)
	return exitOK
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
