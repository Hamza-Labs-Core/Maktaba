package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/models"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

func newSetupCmd() *cobra.Command {
	var runMigrate bool
	var lf layoutFlags
	var nf nodeSetupFlags
	var jsonMode bool
	var step string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive first-run wizard",
		Long: "setup asks the desktop app's setup questions in the terminal (the same steps: check, admin, libraries, Plex, finish) plus where the data goes and the database, then writes server.toml, creates and migrates the database and seeds the admin account." +
			"\n\nWith --self-managed or --service it instead converts this install to the side-by-side layout run by maktaba-launcher (automatic updates) and writes the OS service." +
			"\n\nWith --node it converts this worker-only node into a full node (maktaba-server in node mode, the same layout and launcher).",
		RunE: func(c *cobra.Command, _ []string) error {
			if jsonMode {
				// Epic 36 story 36.6: the app's setup screens, one step per run
				if code := runSetupJSON(step, os.Stdin, os.Stdout); code != 0 {
					os.Exit(code)
				}
				return nil
			}
			if nf.node {
				// Epic 35 story 35.8: a worker-only node becomes a full node
				return runSetupNodeCmd(c, lf)
			}
			if lf.active() {
				return runLayoutSetup(lf)
			}
			return runSetup()
		},
	}
	cmd.Flags().BoolVar(&runMigrate, "migrate", true, "run the initial database migration at the end")
	_ = cmd.Flags().MarkDeprecated("migrate", "setup always prepares the database now (the app's finish step)")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "run one --step for Maktaba.app (JSON on stdin, NDJSON on stdout)")
	cmd.Flags().StringVar(&step, "step", "", "with --json: the step (status)")
	lf.register(cmd)
	nf.register(cmd)
	return cmd
}

// runSetup is the terminal setup of an install outside the desktop app (a
// release, a package): the app's own steps (setup --json: check, admin,
// libraries, plex, finish), run in this process with the terminal's
// questions, so both setups take the same answers and refuse the same ones.
// What the app decides itself the terminal asks first: where the data goes
// and the database (SQLite, or a Postgres url; the app runs its own Postgres).
func runSetup() error { return runSetupWith(newPrompter()) }

func runSetupWith(p *prompter) error {
	cfg, found, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s := &setupRun{ctx: ctx, cfg: cfg, found: found, out: json.NewEncoder(terminalProgress{w: os.Stdout}), log: os.Stderr}
	run := func(step string, in any) (any, error) {
		raw, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		return setupSteps[step](s, raw)
	}
	// a step's refusal is said and the question asked again; anything else ends the setup
	refused := func(err error) bool {
		var se *stepError
		if errors.As(err, &se) && se.field != "" {
			fmt.Printf("  %s\n", se.message)
			return true
		}
		return false
	}

	fmt.Println("Maktaba setup")
	fmt.Printf("Platform: %s/%s\n\n", runtime.GOOS, runtime.GOARCH)

	// where the data goes, and the database (the pending answers live there)
	s.cfg.Server.DataDir = p.line("Data directory (database, models, caches)", s.cfg.Server.DataDir)
	exe, _ := os.Executable()
	if _, bundled, _ := pgbundle.Resolve(s.cfg, exe); !bundled && !s.cfg.DatabaseURLSet {
		fmt.Println("\nStorage backend:")
		fmt.Println("  sqlite   - zero-dependency, great for libraries under ~5 TB (default)")
		fmt.Println("  postgres - recommended for large/multi-user libraries over ~5 TB")
		if strings.ToLower(p.line("Backend (sqlite/postgres)", "sqlite")) == "postgres" {
			s.cfg.Database.URL = p.line("Postgres DSN", "postgres://maktaba:maktaba@localhost:5432/maktaba?sslmode=disable")
		} else {
			s.cfg.Database.URL = "sqlite://" + filepath.Join(s.cfg.Server.DataDir, "maktaba.db")
		}
		s.cfg.DatabaseURLSet = true
	}

	res, err := run("check", struct{}{})
	if err != nil {
		return err
	}
	check := res.(checkResult)
	fmt.Println()
	for _, r := range check.Runs {
		fmt.Printf("  %s\n", r.What)
	}
	for _, n := range check.Warnings {
		fmt.Printf("Note: %s\n", n.Message)
	}
	if !check.OK {
		for _, n := range check.Problems {
			fmt.Printf("Cannot run here: %s\n", n.Message)
		}
		return errors.New("this machine cannot run the server (see above)")
	}

	fmt.Println()
	for {
		user := p.line("Admin username", "admin")
		pass := p.secret("Admin password")
		if p.secret("Password again") != pass {
			fmt.Println("  The two passwords are not the same.")
			continue
		}
		if _, err := run("admin", map[string]string{"username": user, "password": pass}); err != nil {
			if refused(err) {
				continue
			}
			return err
		}
		break
	}

	fmt.Println("\nWhich folders hold your media? (comma-separated)")
	for {
		roots := splitList(p.line("Media roots", strings.Join(s.cfg.Media.Roots, ",")))
		fmt.Println(`If other machines see the same files under another path, give "their path => the path here" (comma-separated; blank: none).`)
		var pathMap []map[string]string
		for _, e := range splitList(p.line("NAS path map", "")) {
			from, to, _ := strings.Cut(e, "=>")
			pathMap = append(pathMap, map[string]string{"from": strings.TrimSpace(from), "to": strings.TrimSpace(to)})
		}
		if _, err := run("libraries", map[string]any{"roots": roots, "path_map": pathMap}); err != nil {
			if refused(err) {
				continue
			}
			return err
		}
		break
	}

	if err := setupPlexCLI(p, run); err != nil {
		return err
	}

	defModel := defaultModelFor(s.cfg.Transcription.Backend)
	if p.yesNo(fmt.Sprintf("\nDownload the Whisper model now (%s)?", defModel), false) {
		if err := models.Download(s.cfg.StateDir(), defModel); err != nil {
			fmt.Fprintf(os.Stderr, "  model download skipped: %v\n", err)
		}
	}

	fmt.Println()
	if _, err := run("finish", struct{}{}); err != nil {
		return err
	}
	fmt.Printf("\nWrote config to %s\n", config.Path())
	fmt.Println("Setup complete. Run `maktaba-server serve` to start.")
	return nil
}

// setupPlexCLI is the optional Plex sign-in (the plex step's PIN flow): the
// code to enter at plex.tv/link, then a wait until it is entered.
func setupPlexCLI(p *prompter, run func(string, any) (any, error)) error {
	if !p.yesNo("\nSign in to Plex now (your Plex libraries and watch history)?", false) {
		_, err := run("plex", map[string]string{"action": "skip"})
		return err
	}
	res, err := run("plex", map[string]string{"action": "pin"})
	if err != nil {
		return err
	}
	pin, _ := res.(map[string]any)
	fmt.Printf("Enter this code at %v: %v\nWaiting for Plex (Ctrl+C to skip)…\n", pin["link_url"], pin["code"])
	for {
		time.Sleep(3 * time.Second)
		res, err := run("plex", map[string]any{"action": "poll", "pin_id": pin["pin_id"]})
		var se *stepError
		switch {
		case errors.As(err, &se) && se.code == "pin_expired":
			fmt.Println("  The Plex code expired: sign in later in Settings › Extensions › Plex.")
			return nil
		case err != nil:
			return err
		}
		if linked, _ := res.(map[string]bool); linked["linked"] {
			fmt.Println("  Plex is linked.")
			return nil
		}
	}
}

// terminalProgress prints the steps' progress lines (the NDJSON the app reads)
// as text: each new message once, with its share when it has one.
type terminalProgress struct{ w io.Writer }

func (t terminalProgress) Write(b []byte) (int, error) {
	var ev setupEvent
	if json.Unmarshal(b, &ev) == nil && ev.Type == "progress" && ev.Message != "" {
		if ev.Total > 0 {
			fmt.Fprintf(t.w, "  %s (%d%%)\n", ev.Message, ev.Done*100/ev.Total)
		} else {
			fmt.Fprintf(t.w, "  %s\n", ev.Message)
		}
	}
	return len(b), nil
}

func splitList(line string) []string {
	var out []string
	for _, r := range strings.Split(line, ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

func defaultModelFor(backend string) string {
	switch backend {
	case "mlx-whisper":
		return "mlx-whisper-large-v3"
	default:
		return "faster-whisper-large-v3"
	}
}

func ensureDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", dir, err)
	}
	return nil
}
