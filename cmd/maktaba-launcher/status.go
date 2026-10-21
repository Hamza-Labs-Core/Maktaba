package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
// the next start (removeOldSelf).
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

// BundleStatus is --status in app-bundle mode: no releases to list.
type BundleStatus struct {
	AppBundle       string `json:"app_bundle"`
	Root            string `json:"root"`
	Updates         string `json:"updates"`
	LauncherVersion string `json:"launcher_version"`
}

func printBundleStatus(w io.Writer, bundle, runtimeRoot, root string, asJSON bool) int {
	s := BundleStatus{AppBundle: bundle, Root: root, Updates: "with the app", LauncherVersion: Version}
	if runtimeRoot != "" && runtimeOwner(runtimeRoot) == runtimeOwnerHeadless {
		s.Updates = "by this machine (maktaba-server update)"
	}
	if asJSON {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(w, string(b))
		return exitOK
	}
	fmt.Fprintf(w, "app bundle: %s\n", s.AppBundle)
	fmt.Fprintf(w, "state:      %s\n", s.Root)
	fmt.Fprintf(w, "updates:    %s\n", s.Updates)
	fmt.Fprintf(w, "launcher:   %s\n", s.LauncherVersion)
	return exitOK
}
