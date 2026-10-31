package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/service"
)

// runtime: the Linux app's runtime copy (Epic 39 story 39.3,
// specs/epics/39-linux-app/contract-runtime.md §3). With --json: setup-style
// NDJSON, progress lines then one result or error line; exit 0 / 1.

// runtimeDeps are what the commands use (tests replace them).
type runtimeDeps struct {
	rt      appruntime.Runtime
	service appruntime.Service // nil: no unit to stop and start
	// homeCeiling is the release this machine's home committed last, and whether it
	// follows a home (update_homecap.go; nil: homeCeilingOf)
	homeCeiling func(ctx context.Context) (string, bool, error)
}

var newRuntimeDeps = func() runtimeDeps {
	d := runtimeDeps{rt: appruntime.New(filepath.Join(config.LocalDirs().Data, "runtime"))}
	if config.Container() != "" {
		return d // the container's entrypoint runs the launcher: no unit to stop and start
	}
	sd := newServiceDeps()
	d.service = unitService{m: sd.manager, u: sd.unit}
	return d
}

// unitService is the systemd user unit as the runtime's Service.
type unitService struct {
	m service.UserManager
	u service.AppUnit
}

func (s unitService) Active(ctx context.Context) bool {
	st := s.m.Status(ctx, s.u)
	return st.Installed && st.Manager == "ok" && st.Active != "inactive" && st.Active != "failed"
}
func (s unitService) Stop(ctx context.Context) error  { return s.m.Control(ctx, "stop") }
func (s unitService) Start(ctx context.Context) error { return s.m.Control(ctx, "start") }

// payloadOfThisBinary is the payload this maktaba-server runs from
// (<payload>/bin/maktaba-server), or "" when it is not in one.
func payloadOfThisBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	dir := filepath.Dir(filepath.Dir(exe))
	if _, err := os.Stat(filepath.Join(dir, appruntime.PayloadJSON)); err != nil {
		return ""
	}
	return dir
}

// runtimeProgress is a --json progress line.
type runtimeProgress struct {
	Type    string `json:"type"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
}

func newRuntimeCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "The Linux app's runtime copy of the server payload",
		Long: "The server runs from a copy of the payload the user owns (<data>/runtime/<version>), never from the\n" +
			"AppImage or the package's files; `current` names the one the service runs.\n\n" +
			"  install    copy a payload in, checking every file (--activate: and switch the service to it)\n" +
			"  activate   switch the service to an installed runtime (stopped while `current` moves)\n" +
			"  status     what is installed, what runs, whether this payload is newer\n" +
			"  prune      remove all but the running runtime and the one before\n\n" +
			"A version older than the newest this machine has run is refused unless --allow-downgrade.",
	}
	cmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "NDJSON progress, then one result or error line (for the app)")

	var from, owner string
	var activate, allowDowngrade bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Copy a payload into the runtime, checking every file",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d := newRuntimeDeps()
			out := c.OutOrStdout()
			return runtimeOut(out, jsonOut, func(ctx context.Context) (any, string, error) {
				src := from
				if src == "" {
					src = payloadOfThisBinary()
				}
				if src == "" {
					return nil, "", &codedError{"payload_invalid", errors.New("this maktaba-server is not in a payload: give --from DIR")}
				}
				p, err := appruntime.ReadPayload(src)
				if err != nil {
					return nil, "", err
				}
				opt := appruntime.Options{AllowDowngrade: allowDowngrade}
				report := progressReporter(out, jsonOut, "copy", "Copying Maktaba "+p.Version)
				r, err := d.rt.Install(ctx, src, report, opt)
				if err != nil {
					return nil, "", err
				}
				if owner != "" {
					if err := d.rt.SetOwner(owner); err != nil {
						return nil, "", err
					}
				}
				res := map[string]any{"version": p.Version, "dir": r.Dir, "copied": r.Copied, "activated": r.First}
				text := fmt.Sprintf("installed %s in %s", p.Version, r.Dir)
				cur, _ := d.rt.Current()
				switch {
				case r.First:
					text += " (current: the first runtime here)"
				case activate && cur != filepath.Base(r.Dir):
					prev, err := d.rt.Activate(ctx, filepath.Base(r.Dir), d.service, opt)
					if err != nil {
						return nil, "", err
					}
					pruned, err := d.rt.Prune(ctx, 2)
					if err != nil {
						return nil, "", err
					}
					res["activated"], res["previous"], res["pruned"] = true, prev, pruned
					text += " and switched to it"
				}
				return res, text, nil
			})
		},
	}
	install.Flags().StringVar(&from, "from", "", "the payload directory (default: the one this binary is in)")
	install.Flags().StringVar(&owner, "owner", "", "who updates it: app (the desktop app) or headless (the machine itself)")
	install.Flags().BoolVar(&activate, "activate", false, "switch the service to it, then prune")
	install.Flags().BoolVar(&allowDowngrade, "allow-downgrade", false,
		"take a version older than the newest this machine has run (refused otherwise: its database may be migrated past it)")

	var activateDowngrade bool
	activateCmd := &cobra.Command{
		Use:   "activate <dir|previous>",
		Short: "Switch the service to an installed runtime (previous: the one that ran before)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			d := newRuntimeDeps()
			return runtimeOut(c.OutOrStdout(), jsonOut, func(ctx context.Context) (any, string, error) {
				prev, err := d.rt.Activate(ctx, args[0], d.service, appruntime.Options{AllowDowngrade: activateDowngrade})
				if err != nil {
					return nil, "", err
				}
				cur, _ := d.rt.Current()
				return map[string]any{"current": cur, "previous": prev},
					fmt.Sprintf("current: %s (was %s)", cur, orNone(prev)), nil
			})
		},
	}
	activateCmd.Flags().BoolVar(&activateDowngrade, "allow-downgrade", false,
		"switch to a version older than the newest this machine has run (a rollback)")

	var statusFrom string
	status := &cobra.Command{
		Use:   "status",
		Short: "What is installed, what runs, whether this payload is newer",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d := newRuntimeDeps()
			return runtimeOut(c.OutOrStdout(), jsonOut, func(context.Context) (any, string, error) {
				src := statusFrom
				if src == "" {
					src = payloadOfThisBinary()
				}
				st, err := d.rt.Status(src)
				if err != nil {
					return nil, "", err
				}
				text := fmt.Sprintf("runtime %s: current %s (%s), owner %s, %d installed", st.Root, orNone(st.Current),
					orNone(st.Version), st.Owner, len(st.Installed))
				switch {
				case st.UpdatePending:
					text += fmt.Sprintf("; this payload (%s) is newer than current", st.FromVersion)
				case st.Downgrade:
					text += fmt.Sprintf("; this payload (%s) is older than %s, the newest this machine has run", st.FromVersion, st.Watermark)
				}
				return st, text, nil
			})
		},
	}

	status.Flags().StringVar(&statusFrom, "from", "", "the payload to compare with current (default: the one this binary is in)")

	var keep int
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Remove all but the running runtime and the one before",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d := newRuntimeDeps()
			return runtimeOut(c.OutOrStdout(), jsonOut, func(ctx context.Context) (any, string, error) {
				removed, err := d.rt.Prune(ctx, keep)
				if err != nil {
					return nil, "", err
				}
				return map[string]any{"removed": removed}, "removed " + orNone(strings.Join(removed, ", ")), nil
			})
		},
	}
	prune.Flags().IntVar(&keep, "keep", 2, "how many to keep, the running one included (at least 2)")

	cmd.AddCommand(install, activateCmd, status, prune)
	return cmd
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// progressReporter writes byte progress: --json lines at most twice a second
// (and the last one), else nothing (the copy takes seconds).
func progressReporter(out io.Writer, jsonOut bool, stage, message string) appruntime.Progress {
	if !jsonOut {
		return nil
	}
	var mu sync.Mutex
	var last time.Time
	enc := json.NewEncoder(out)
	return func(done, total int64) {
		mu.Lock()
		defer mu.Unlock()
		if done < total && time.Since(last) < 500*time.Millisecond {
			return
		}
		last = time.Now()
		_ = enc.Encode(runtimeProgress{Type: "progress", Stage: stage, Message: message, Done: done, Total: total})
	}
}

// runtimeCode is the --json error code of an error.
func runtimeCode(err error) string {
	var ce *codedError
	switch {
	case errors.As(err, &ce):
		return ce.code
	case errors.Is(err, appruntime.ErrInvalid):
		return "payload_invalid"
	case errors.Is(err, appruntime.ErrDiskFull):
		return "disk_full"
	case errors.Is(err, appruntime.ErrLocked):
		return "locked"
	case errors.Is(err, appruntime.ErrNotInstalled):
		return "not_installed"
	case errors.Is(err, appruntime.ErrDowngrade):
		return "downgrade"
	case errors.Is(err, appruntime.ErrService):
		return "service"
	}
	return "runtime"
}

// runtimeOut runs f (bounded: a stop waits for the unit's 90 s, a copy of a
// few hundred MB) and reports it as a line of text, or as an NDJSON event.
func runtimeOut(out io.Writer, jsonOut bool, f func(ctx context.Context) (any, string, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	data, text, err := f(ctx)
	if !jsonOut {
		if err != nil {
			return err
		}
		_, werr := fmt.Fprintln(out, text)
		return werr
	}
	enc := json.NewEncoder(out)
	if err != nil {
		_ = enc.Encode(setupEvent{Type: "error", Code: runtimeCode(err), Message: err.Error()})
		os.Exit(1)
	}
	return enc.Encode(setupEvent{Type: "result", Data: data})
}
