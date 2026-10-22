package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func newUninstallCmd() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Guided teardown of a Maktaba install",
		Long: `uninstall stops the running service, then (interactively, unless
--purge) asks whether to keep your data and config before removing the
binary and service files.

--purge removes everything — binary, service files, config, AND data —
without prompting. There is no undo.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runUninstall(purge)
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "non-interactive full removal (binary, config, AND data)")
	return cmd
}

func runUninstall(purge bool) error {
	cfg, _, _ := config.Load()

	fmt.Println("Stopping any running Maktaba service ...")
	stopService()

	removeConfig := purge
	removeData := purge
	if !purge {
		p := newPrompter()
		fmt.Println("\nThis will remove the maktaba-server binary and service files.")
		keepData := p.yesNo("Keep your media database and downloaded models?", true)
		keepConfig := p.yesNo("Keep your server.toml config?", true)
		removeData = !keepData
		removeConfig = !keepConfig
		if !p.yesNo("\nProceed with uninstall?", false) {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Service unit files.
	for _, f := range serviceFiles() {
		removeIfExists(f, "service file")
	}

	exe, _ := os.Executable()
	if removeConfig {
		purgeConfig(config.Path(), config.KeysDir(), config.WindowsOriginals(exe))
	}

	if removeData {
		// read before the data goes: on Windows the state root is recorded (StateDir)
		root := cfg.StateDir()
		if cfg.Server.DataDir != "" {
			removeTreeIfExists(cfg.Server.DataDir, "data dir")
		}
		// on a Mac, and a Windows install of Epic 38 on, the large state sits beside
		// data/ (config.StateDir)
		if root != cfg.Server.DataDir {
			for _, d := range []string{"models", "chroma", "cache", "postgres", "backups"} {
				removeTreeIfExists(filepath.Join(root, d), d)
			}
		}
		if rec := config.StateRecordPath(exe); rec != "" {
			removeIfExists(rec, "state root record")
		}
	}

	// The binary itself, last, so earlier steps can still read config.
	if self, err := os.Executable(); err == nil {
		self, _ = filepath.EvalSymlinks(self)
		// On Unix we can unlink a running binary; on Windows this fails
		// while running, so leave a hint.
		if err := os.Remove(self); err != nil {
			fmt.Printf("Could not remove binary %s now (%v).\n", self, err)
			if runtime.GOOS == "windows" {
				fmt.Println("Delete it manually after this process exits.")
			}
		} else {
			fmt.Printf("Removed binary %s\n", self)
		}
	}

	fmt.Println("\nUninstall complete.")
	return nil
}

// stopService best-effort stops the platform service manager's units.
// It targets both the unified `maktaba-server` unit and the legacy
// per-service units (maktaba-api/streaming/pipeline) so a teardown works
// regardless of which deployment model was installed. Errors are
// intentionally swallowed: a unit may not exist (manual install), or we
// may lack privileges — neither should block teardown.
func stopService() {
	switch runtime.GOOS {
	case "linux":
		units := append([]string{"maktaba-server"}, legacyUnits...)
		_ = exec.Command("systemctl", append([]string{"stop"}, units...)...).Run()
	case "darwin":
		for _, label := range append([]string{"com.maktaba.server"}, legacyLaunchdLabels...) {
			_ = exec.Command("launchctl", "bootout", "system/"+label).Run()
		}
	case "windows":
		_ = exec.Command("sc", "stop", "MaktabaServer").Run()
	}
}

// legacyUnits / legacyLaunchdLabels are the per-service deployment's
// names, matched to deploy/packaging/{systemd,launchd}.
var (
	legacyUnits         = []string{"maktaba-api", "maktaba-streaming", "maktaba-pipeline"}
	legacyLaunchdLabels = []string{"com.maktaba.api", "com.maktaba.streaming", "com.maktaba.pipeline"}
)

// serviceFiles lists the per-platform unit file locations to remove —
// the unified unit plus the legacy per-service units.
func serviceFiles() []string {
	switch runtime.GOOS {
	case "linux":
		out := []string{
			"/etc/systemd/system/maktaba-server.service",
			filepath.Join(home(), ".config/systemd/user/maktaba-server.service"),
		}
		for _, u := range legacyUnits {
			out = append(out, "/etc/systemd/system/"+u+".service")
		}
		return out
	case "darwin":
		out := []string{
			"/Library/LaunchDaemons/com.maktaba.server.plist",
			filepath.Join(home(), "Library/LaunchAgents/com.maktaba.server.plist"),
		}
		for _, l := range legacyLaunchdLabels {
			out = append(out, "/Library/LaunchDaemons/"+l+".plist")
		}
		return out
	default:
		return nil
	}
}

// purgeConfig removes server.toml (path), the keys directory when it is not the
// config's own folder (a Mac's and Windows' keys/), the folder when that leaves it
// empty, and on Windows the originals of an install from before Epic 38 that the
// first command copied (config.WindowsOriginals): left behind, the next install's
// first command would copy them back.
func purgeConfig(path, keys string, originals []string) {
	removeIfExists(path, "config")
	if keys != filepath.Dir(path) {
		removeTreeIfExists(keys, "keys dir")
	}
	for _, p := range originals {
		removeIfExists(p, "config from before Epic 38")
	}
	_ = os.Remove(filepath.Dir(path))
	for _, p := range originals {
		_ = os.Remove(filepath.Dir(p)) // %APPDATA%\Maktaba, once nothing is left in it
	}
}

func removeIfExists(path, label string) {
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			fmt.Printf("Could not remove %s %s: %v\n", label, path, err)
		} else {
			fmt.Printf("Removed %s %s\n", label, path)
		}
	}
}

func removeTreeIfExists(path, label string) {
	if _, err := os.Stat(path); err == nil {
		if err := os.RemoveAll(path); err != nil {
			fmt.Printf("Could not remove %s %s: %v\n", label, path, err)
		} else {
			fmt.Printf("Removed %s %s\n", label, path)
		}
	}
}

func home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}
