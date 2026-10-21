// Command maktaba-launcher is the small, stable process the OS service runs on
// every Maktaba machine (Epic 35, story 35.2, design §2.2). It starts
// <install>/bin/releases/<current>/maktaba-server serve, restarts it on exit
// code 75, restarts it with backoff on a crash, and falls back to the
// previous release when a pending (unconfirmed) one cannot boot: 3 exits
// within 10 minutes, or no confirmation within 15 minutes. All of its state
// is in the pointer files, so a power cut at any moment of an update comes
// back up on a release that works.
//
// It never downloads, verifies signatures or touches the database; it uses
// only the standard library and golang.org/x/sys.
//
//	maktaba-launcher [--root DIR] serve [-- server args]
//	maktaba-launcher --version
//	maktaba-launcher --status [--json]
//	maktaba-launcher --verify <release dir>
//	maktaba-launcher --replace-self <installed launcher path>
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// LauncherProtocol is release.json's launcher_protocol: a release that needs
// a newer launcher replaces it (story 35.7).
const LauncherProtocol = 1

// Version is stamped at build time (-X main.Version=…).
var Version = "dev"

// Exit codes of the launcher itself.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("maktaba-launcher", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		root        = fs.String("root", "", "install root (default: $MAKTABA_INSTALL_ROOT, else the launcher's directory)")
		showVersion = fs.Bool("version", false, "print the launcher version and protocol")
		status      = fs.Bool("status", false, "print current, previous, pending, watermark and the last rollback")
		asJSON      = fs.Bool("json", false, "with --status: JSON output")
		verify      = fs.String("verify", "", "re-check a release directory's files against its .verified record")
		replaceSelf = fs.String("replace-self", "", "install this executable over the given launcher path (story 35.7)")
		hideConsole = fs.Bool("hide-console", false, "Windows logon task: hide the console window")
		pendingTO   = fs.Duration("pending-timeout", 15*time.Minute, "roll back a pending release not confirmed within this")
		stopGrace   = fs.Duration("stop-grace", time.Minute, "how long the server may take to stop before it is killed")
		appBundle   = fs.String("app-bundle", "", "run the server payload in DIR (default: detected when the launcher is inside Maktaba.app)")
	)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *showVersion {
		fmt.Fprintf(stdout, "maktaba-launcher %s (launcher_protocol %d)\n", Version, LauncherProtocol)
		return exitOK
	}
	bundle := *appBundle
	if bundle == "" {
		bundle, _ = bundlePayload(selfDir())
	}
	if bundle != "" {
		return runBundle(fs.Args(), stdout, stderr, bundle, appStateRoot(*root), *status, *asJSON,
			*replaceSelf != "", *stopGrace)
	}
	l := layout.New(resolveRoot(*root))
	switch {
	case *status:
		return printStatus(stdout, l, *asJSON)
	case *verify != "":
		return runVerify(stdout, stderr, *verify)
	case *replaceSelf != "":
		if err := ReplaceSelf(*replaceSelf); err != nil {
			fmt.Fprintf(stderr, "maktaba-launcher: replace-self: %v\n", err)
			return exitFailure
		}
		fmt.Fprintf(stdout, "installed %s\n", *replaceSelf)
		return exitOK
	}

	rest := fs.Args()
	if len(rest) == 0 || rest[0] != "serve" {
		fmt.Fprintln(stderr, "usage: maktaba-launcher [--root DIR] serve [-- server args] | --version | --status | --verify DIR | --replace-self PATH")
		return exitUsage
	}
	serverArgs := rest[1:]
	if len(serverArgs) > 0 && serverArgs[0] == "--" {
		serverArgs = serverArgs[1:]
	}
	if !layout.IsLayout(l.Root) {
		fmt.Fprintf(stderr, "maktaba-launcher: %s is not a self-managed install (no current pointer); run `maktaba-server setup --self-managed`\n", l.Root)
		return exitFailure
	}
	cfg := Config{Root: l.Root, Args: serverArgs, PendingTimeout: *pendingTO, StopGrace: *stopGrace,
		Stdout: stdout, Stderr: stderr}

	if isService() {
		setupConsole(true, true)
		if err := runService(cfg); err != nil {
			fmt.Fprintf(stderr, "maktaba-launcher: service: %v\n", err)
			return exitFailure
		}
		return exitOK
	}
	setupConsole(*hideConsole, false)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, cfg); err != nil {
		if errors.Is(err, layout.ErrLocked) {
			return exitFailure
		}
		fmt.Fprintf(stderr, "maktaba-launcher: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// runBundle is run in app-bundle mode (bundle.go). The app updater replaces
// the launcher with the rest of the app, so --replace-self is refused.
func runBundle(rest []string, stdout, stderr io.Writer, bundle, root string, status, asJSON, replaceSelf bool,
	stopGrace time.Duration) int {
	switch {
	case replaceSelf:
		fmt.Fprintln(stderr, "maktaba-launcher: replace-self: this launcher is part of Maktaba.app; it is updated with the app")
		return exitFailure
	case status:
		return printBundleStatus(stdout, bundle, root, asJSON)
	}
	if len(rest) == 0 || rest[0] != "serve" {
		fmt.Fprintln(stderr, "usage: maktaba-launcher [--app-bundle DIR] [--root DIR] serve [-- server args] | --version | --status")
		return exitUsage
	}
	serverArgs := rest[1:]
	if len(serverArgs) > 0 && serverArgs[0] == "--" {
		serverArgs = serverArgs[1:]
	}
	if _, err := os.Stat(filepath.Join(bundle, "bin", layout.Exe("maktaba-server"))); err != nil {
		fmt.Fprintf(stderr, "maktaba-launcher: %s has no bin/maktaba-server: %v\n", bundle, err)
		return exitFailure
	}
	if err := os.MkdirAll(layout.New(root).UpdatesDir(), 0o755); err != nil {
		fmt.Fprintf(stderr, "maktaba-launcher: state directory: %v\n", err)
		return exitFailure
	}
	cfg := Config{Root: root, Bundle: bundle, Args: serverArgs, StopGrace: stopGrace, Stdout: stdout, Stderr: stderr}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, cfg); err != nil {
		if !errors.Is(err, layout.ErrLocked) {
			fmt.Fprintf(stderr, "maktaba-launcher: %v\n", err)
		}
		return exitFailure
	}
	return exitOK
}

// resolveRoot: --root, then $MAKTABA_INSTALL_ROOT, then the launcher's own
// directory (<install>/maktaba-launcher).
func resolveRoot(flagRoot string) string {
	if flagRoot != "" {
		return flagRoot
	}
	if r := os.Getenv(layout.EnvInstallRoot); r != "" {
		return r
	}
	if self, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		return filepath.Dir(self)
	}
	return "."
}

func runVerify(stdout, stderr io.Writer, dir string) int {
	v, err := layout.VerifyFiles(dir)
	if err != nil {
		fmt.Fprintf(stderr, "maktaba-launcher: verify %s: %v\n", dir, err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "%s: %d files match .verified (identity %q, verified %s)\n",
		dir, len(v.SHA256), v.Identity, v.VerifiedAt.Format(time.RFC3339))
	return exitOK
}
