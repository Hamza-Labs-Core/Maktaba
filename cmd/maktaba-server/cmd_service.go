package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/service"
)

// service: the Linux app's systemd user unit (Epic 39 story 39.5,
// specs/epics/39-linux-app/contract-runtime.md §5). The desktop shell and the
// terminal setup call it; neither writes a unit itself. With --json, one
// NDJSON line, setup's `result` or `error` event: exit 0 with a result, 1 with
// an error.

// serviceDeps are what the commands use (tests replace them).
type serviceDeps struct {
	manager service.UserManager
	unit    service.AppUnit
}

var newServiceDeps = func() serviceDeps {
	home, _ := os.UserHomeDir()
	m := service.LocalUserManager()
	u := service.AppUnitFor(os.Getenv, home, config.LocalDirs().Data, config.LogsDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u.Dir = m.UnitDir(ctx, u.Dir) // where the user's systemd looks, not a shell's XDG_CONFIG_HOME
	return serviceDeps{manager: m, unit: u}
}

func newServiceCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "service",
		Short: "The server's systemd user service (the Linux app)",
		Long: "service installs and controls the systemd user unit that runs this machine's server from the app's\n" +
			"runtime copy (" + "<data>/runtime/current" + "), started at boot when linger is on.\n\n" +
			"  install    write the unit, enable it and start it\n" +
			"  uninstall  stop, disable and remove it (the data stays)\n" +
			"  start | stop | restart\n" +
			"  linger     let it start at boot without a login (--off: only while logged in)\n" +
			"  status     what is installed and running",
	}
	cmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "one NDJSON result or error line on stdout (for the app)")

	var start, replace bool
	var launcher, logFile string
	install := &cobra.Command{
		Use:   "install",
		Short: "Write the unit, enable it and start it",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d := newServiceDeps()
			if launcher != "" {
				d.unit.Launcher = launcher
			}
			if logFile != "" {
				d.unit.LogFile = logFile
			}
			return serviceOut(c.OutOrStdout(), jsonOut, func(ctx context.Context) (any, string, error) {
				if info, err := os.Stat(d.unit.Launcher); err != nil || info.IsDir() {
					return nil, "", &codedError{"runtime_missing", fmt.Errorf("no runtime at %s: install it first "+
						"(maktaba-server runtime install)", d.unit.Launcher)}
				}
				if err := d.manager.Install(ctx, d.unit, start, replace); err != nil {
					return nil, "", err
				}
				st := d.manager.Status(ctx, d.unit)
				return st, fmt.Sprintf("installed %s (%s)", st.Unit, st.Active), nil
			})
		},
	}
	install.Flags().BoolVar(&start, "start", true, "start it now")
	install.Flags().BoolVar(&replace, "replace", false, "replace another install's maktaba-server.service")
	install.Flags().StringVar(&launcher, "launcher", "", "the launcher the unit runs (default: <data>/runtime/current/bin/maktaba-launcher)")
	install.Flags().StringVar(&logFile, "log-file", "", "the server's log (default: <logs>/server.log)")

	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop, disable and remove the unit (the data stays)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d := newServiceDeps()
			return serviceOut(c.OutOrStdout(), jsonOut, func(ctx context.Context) (any, string, error) {
				if err := d.manager.Uninstall(ctx, d.unit); err != nil {
					return nil, "", err
				}
				return d.manager.Status(ctx, d.unit), "removed " + d.unit.Path(), nil
			})
		},
	}

	control := func(action, short string) *cobra.Command {
		return &cobra.Command{
			Use:   action,
			Short: short,
			Args:  cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				d := newServiceDeps()
				return serviceOut(c.OutOrStdout(), jsonOut, func(ctx context.Context) (any, string, error) {
					if st := d.manager.Status(ctx, d.unit); !st.Installed {
						return nil, "", &codedError{"not_installed", errors.New("the service is not installed (maktaba-server service install)")}
					}
					if err := d.manager.Control(ctx, action); err != nil {
						return nil, "", err
					}
					st := d.manager.Status(ctx, d.unit)
					return st, fmt.Sprintf("%s: %s", action, st.Active), nil
				})
			},
		}
	}

	var off bool
	linger := &cobra.Command{
		Use:   "linger",
		Short: "Start the service at boot, without a login (--off: only while logged in)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d := newServiceDeps()
			return serviceOut(c.OutOrStdout(), jsonOut, func(ctx context.Context) (any, string, error) {
				if err := d.manager.Linger(ctx, !off); err != nil {
					return nil, "", &codedError{"linger", fmt.Errorf("%w (an administrator can run: loginctl enable-linger %s)", err, os.Getenv("USER"))}
				}
				st := d.manager.Status(ctx, d.unit)
				return st, fmt.Sprintf("linger: %v", st.Linger), nil
			})
		},
	}
	linger.Flags().BoolVar(&off, "off", false, "turn linger off")

	status := &cobra.Command{
		Use:   "status",
		Short: "What is installed and running",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d := newServiceDeps()
			return serviceOut(c.OutOrStdout(), jsonOut, func(ctx context.Context) (any, string, error) {
				st := d.manager.Status(ctx, d.unit)
				return st, describeService(st), nil
			})
		},
	}

	cmd.AddCommand(install, uninstall,
		control("start", "Start the service"),
		control("stop", "Stop the service (it stays stopped until start or the next boot)"),
		control("restart", "Restart the service"),
		linger, status)
	return cmd
}

// codedError carries the machine code of a --json error.
type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

// serviceCode is the --json error code of an error.
func serviceCode(err error) string {
	var ce *codedError
	switch {
	case errors.As(err, &ce):
		return ce.code
	case errors.Is(err, service.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, service.ErrServiceTaken):
		return "service_taken"
	}
	return "systemd"
}

// serviceOut runs f (bounded: systemd's stop waits for the unit's 90 s) and
// reports it as a line of text, or with jsonOut as one NDJSON event.
func serviceOut(out io.Writer, jsonOut bool, f func(ctx context.Context) (any, string, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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
		_ = enc.Encode(setupEvent{Type: "error", Code: serviceCode(err), Message: err.Error()})
		os.Exit(1)
	}
	return enc.Encode(setupEvent{Type: "result", Data: data})
}

func describeService(st service.AppStatus) string {
	switch {
	case st.Manager != "ok":
		return "unsupported: " + st.Reason
	case st.Taken != "":
		return fmt.Sprintf("%s belongs to another install (it runs %s)", st.Unit, st.Taken)
	case st.SystemUnit != "" && !st.Installed:
		return fmt.Sprintf("not installed: a system-wide Maktaba service is (%s)", st.SystemUnit)
	case !st.Installed:
		return "not installed"
	}
	s := fmt.Sprintf("%s: %s", st.Unit, st.Active)
	if st.SubState != "" {
		s += " (" + st.SubState + ")"
	}
	if st.Enabled {
		s += ", enabled"
	}
	if st.Linger {
		s += ", starts at boot (linger)"
	} else {
		s += ", runs only while you are logged in (no linger)"
	}
	return s
}
