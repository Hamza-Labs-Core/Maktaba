package main

// `maktaba-server setup --self-managed` and `setup --service` (Epic 35, story
// 35.2 AC7, AC8, design §2.3, §2.4).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/selfmanaged"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/service"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

type layoutFlags struct {
	selfManaged bool
	service     bool
	user        bool
	system      bool
	winService  bool
	noService   bool
	dryRun      bool
	root        string
	from        string
	version     string
	identity    string
	launcher    string
	config      string
}

func (f *layoutFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.BoolVar(&f.selfManaged, "self-managed", false, "convert this install to the side-by-side layout run by maktaba-launcher (bin/releases/<version>/)")
	fl.BoolVar(&f.service, "service", false, "write (and enable) the OS service that runs maktaba-launcher")
	fl.BoolVar(&f.user, "user", false, "per-user service: systemd user unit, LaunchAgent, Windows logon task (Windows default)")
	fl.BoolVar(&f.system, "system", false, "system service: systemd system unit, LaunchDaemon, Windows service")
	fl.BoolVar(&f.winService, "windows-service", false, "Windows: a service instead of the logon task (same as --system)")
	fl.BoolVar(&f.noService, "no-service", false, "with --self-managed: do not write the OS service")
	fl.BoolVar(&f.dryRun, "dry-run", false, "print what would be written and run, change nothing")
	fl.StringVar(&f.root, "root", "", "install root (default per OS and mode, e.g. /var/lib/maktaba, %LOCALAPPDATA%\\Maktaba)")
	fl.StringVar(&f.from, "from", "", "directory holding the binaries to install (default: this executable's directory)")
	fl.StringVar(&f.version, "version", "", "version of those binaries (default: this build's version)")
	fl.StringVar(&f.identity, "identity", selfmanaged.IdentityMigrated, "identity recorded in .verified (migrated|package)")
	fl.StringVar(&f.launcher, "launcher", "", "launcher path the service runs (default <root>/maktaba-launcher; packages: /usr/bin/maktaba-launcher)")
	fl.StringVar(&f.config, "config", "", "server.toml the service exports as MAKTABA_CONFIG (default: the resolved config path)")
	_ = fl.MarkHidden("identity")
}

func (f layoutFlags) active() bool { return f.selfManaged || f.service }

// mode: --system / --user, else the OS default (Linux as root: system;
// macOS and Windows: the user's session, where the share and drives are).
func (f layoutFlags) mode() layout.Mode {
	switch {
	case f.system || f.winService:
		return layout.ModeSystem
	case f.user:
		return layout.ModeUser
	case runtime.GOOS == "linux" && os.Geteuid() == 0:
		return layout.ModeSystem
	default:
		return layout.ModeUser
	}
}

func runLayoutSetup(f layoutFlags) error {
	if f.system && f.user {
		return errors.New("--system and --user are exclusive")
	}
	mode := f.mode()
	root := f.root
	if root == "" {
		root = layout.DefaultRoot(mode)
	}
	from := f.from
	if from == "" {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		from = filepath.Dir(self)
	}
	ver := f.version
	if ver == "" {
		ver = version.Version
	}
	logf := func(s string) { fmt.Println("  " + s) }

	if f.selfManaged {
		if selfmanaged.HomebrewManaged(from) {
			fmt.Printf("This install is managed by Homebrew (%s): automatic updates are off for it.\n", from)
			fmt.Printf("Converting copies it to %s; stop Homebrew's service first: brew services stop maktaba\n", root)
		}
		fmt.Printf("Self-managed install at %s (from %s, version %s)\n", root, from, ver)
		if f.dryRun {
			fmt.Printf("  (dry run) would stage bin/releases/%s/, write current/watermark, install maktaba-launcher\n",
				layout.NormalizeVersion(ver))
		} else {
			res, err := selfmanaged.Convert(selfmanaged.Options{Root: root, From: from, Version: ver,
				Identity: f.identity, Log: logf})
			if err != nil {
				return err
			}
			if res.Pending {
				fmt.Printf("  %s is pending: the launcher falls back to the previous release unless it runs healthy\n", res.Version)
			}
		}
		if f.noService {
			return nil
		}
	}

	cfgPath := f.config
	if cfgPath == "" {
		cfgPath = config.Path()
	}
	if err := checkLayoutPorts(cfgPath, f.dryRun); err != nil {
		return err
	}
	spec := service.Spec{
		Kind:       service.DefaultKind(runtime.GOOS, mode),
		Mode:       mode,
		Root:       root,
		Launcher:   f.launcher,
		ConfigPath: cfgPath,
	}
	steps, err := service.Plan(spec)
	if err != nil {
		return err
	}
	fmt.Printf("Service (%s, %s):\n", spec.Kind, mode)
	if f.dryRun {
		for _, st := range steps {
			fmt.Println("  " + st.String())
			if st.Content != "" {
				fmt.Println(indent(st.Content))
			}
		}
		if spec.Kind == service.KindWinService {
			body, _ := service.Render(spec)
			fmt.Println(indent(body))
		}
		return nil
	}
	if err := service.Install(spec, logf); err != nil {
		return err
	}
	fmt.Println("Done. Start it with:", startHint(spec))
	return nil
}

func startHint(s service.Spec) string {
	switch s.Kind {
	case service.KindSystemd:
		if s.Mode == layout.ModeSystem {
			return "sudo systemctl restart " + service.Name
		}
		return "systemctl --user restart " + service.Name
	case service.KindLaunchd:
		return "launchctl kickstart -k " + map[bool]string{true: "system", false: "gui/$(id -u)"}[s.Mode == layout.ModeSystem] + "/" + service.LaunchdLabel
	case service.KindTask:
		return "schtasks /Run /TN " + service.WindowsName
	default:
		return "sc start " + service.WindowsName
	}
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return "      " + strings.Join(lines, "\n      ") + "\n"
}

// checkLayoutPorts runs the port check of setup on the install's server.toml (a
// missing one is left to the first serve): a web, API or streaming port another
// program holds moves into the fallback range and is written back.
func checkLayoutPorts(cfgPath string, dryRun bool) error {
	cfg, found, err := config.LoadFrom(cfgPath)
	if err != nil || !found {
		return err
	}
	if dryRun {
		moved, err := cfg.ReconcilePorts(portProbe(), config.PortOptions{KeepOurs: true})
		for _, m := range moved {
			fmt.Printf("  (dry run) port: would move %s\n", m)
		}
		return err
	}
	_, err = reconcilePorts(&cfg, cfgPath, config.PortOptions{KeepOurs: true}, os.Stdout, true)
	return err
}
