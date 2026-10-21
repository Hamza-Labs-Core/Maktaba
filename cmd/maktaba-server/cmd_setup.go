package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/models"
)

func newSetupCmd() *cobra.Command {
	var runMigrate bool
	var lf layoutFlags
	var nf nodeSetupFlags
	var jsonMode bool
	var step string
	var answersFile string
	var answersFD int
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive first-run wizard",
		Long: "setup detects your platform, gathers media roots and storage choice, seeds an admin credential, optionally downloads a Whisper model, writes server.toml, and runs the initial migration." +
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
			// Epic 39 story 39.6: run from an app payload with no window (a Linux
			// machine without a desktop), the app's setup steps in the terminal
			if payloadOfThisBinary() != "" {
				return runAppSetupCmd(answersFile, answersFD)
			}
			return runSetup(runMigrate)
		},
	}
	cmd.Flags().BoolVar(&runMigrate, "migrate", true, "run the initial database migration at the end")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "run one --step for Maktaba.app (JSON on stdin, NDJSON on stdout)")
	cmd.Flags().StringVar(&step, "step", "", "with --json: the step (status)")
	cmd.Flags().StringVar(&answersFile, "answers", "", "from a payload: the steps' inputs from this JSON file ({\"admin\": {...}, ...}) instead of prompts")
	cmd.Flags().IntVar(&answersFD, "answers-fd", 0, "from a payload: the same answers from this open file descriptor (secrets never on disk)")
	lf.register(cmd)
	nf.register(cmd)
	return cmd
}

func runSetup(doMigrate bool) error {
	p := newPrompter()
	cfg := config.Defaults()

	fmt.Println("Maktaba setup")
	fmt.Printf("Platform: %s/%s\n\n", runtime.GOOS, runtime.GOARCH)

	// 1. Media roots.
	fmt.Println("Which directories hold your media? (comma-separated, blank to skip)")
	rootsLine := p.line("Media roots", strings.Join(cfg.Media.Roots, ","))
	cfg.Media.Roots = splitRoots(rootsLine)

	// 2. Data dir.
	cfg.Server.DataDir = p.line("Data directory (database, models, caches)", cfg.Server.DataDir)

	// 3. Storage backend.
	fmt.Println("\nStorage backend:")
	fmt.Println("  sqlite   - zero-dependency, great for libraries under ~5 TB (default)")
	fmt.Println("  postgres - recommended for large/multi-user libraries over ~5 TB")
	backend := strings.ToLower(p.line("Backend (sqlite/postgres)", "sqlite"))
	if backend == "postgres" {
		def := "postgres://maktaba:maktaba@localhost:5432/maktaba?sslmode=disable"
		cfg.Database.URL = p.line("Postgres DSN", def)
	} else {
		cfg.Database.URL = "sqlite://" + filepath.Join(cfg.Server.DataDir, "maktaba.db")
	}

	// 4. Admin credential: a username/password pair, or a single-user
	// admin token. The token path is handy for headless single-user
	// installs where there's no human to type a password later.
	useToken := p.yesNo("\nSingle-user install? (generate an admin token instead of a username/password)", false)
	var adminUser, adminPass, adminToken string
	if useToken {
		adminToken = generateToken()
	} else {
		adminUser = p.line("Admin username", "admin")
		for {
			adminPass = p.secret("Admin password")
			if len(adminPass) >= 8 {
				break
			}
			fmt.Println("  Password must be at least 8 characters.")
		}
	}

	// 5. Transcription backend + optional model download.
	fmt.Printf("\nTranscription backend [%s] selected for this hardware.\n", cfg.Transcription.Backend)
	cfg.Transcription.Backend = p.line("Transcription backend (mlx-whisper/faster-whisper/openai)", cfg.Transcription.Backend)
	defModel := defaultModelFor(cfg.Transcription.Backend)
	if p.yesNo(fmt.Sprintf("Download the Whisper model now (%s)?", defModel), false) {
		if err := models.Download(cfg.StateDir(), defModel); err != nil {
			fmt.Fprintf(os.Stderr, "  model download skipped: %v\n", err)
		}
	}

	// 6. Write config.
	if err := ensureDataDir(cfg.Server.DataDir); err != nil {
		return err
	}
	// The web, API and streaming ports another program holds move into the
	// fallback range before the config is written (config/ports.go).
	if _, err := reconcilePorts(&cfg, config.Path(), config.PortOptions{KeepOurs: true}, os.Stdout, false); err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("\nWrote config to %s\n", config.Path())

	// 7. Initial migration + admin seeding.
	if doMigrate {
		fmt.Println("Running initial migration ...")
		if err := runAPISub("migrate", []string{"up"}); err != nil {
			fmt.Fprintf(os.Stderr, "migration failed (run `maktaba-server migrate up` once the api binary is on PATH): %v\n", err)
		} else if !useToken {
			fmt.Println("Seeding admin user ...")
			if err := seedAdminCLI(adminUser, adminPass); err != nil {
				fmt.Fprintf(os.Stderr, "adduser failed (run `maktaba-server adduser` later): %v\n", err)
			}
		}
	}

	// 8. Done.
	fmt.Println("\nSetup complete. Run `maktaba-server serve` to start.")
	if useToken {
		fmt.Printf("\nAdmin token (store it safely — shown once):\n  %s\n", adminToken)
	}
	return nil
}

// seedAdminCLI runs `maktaba-api adduser --password-file <f> <user>`: the api
// takes the username as its argument and the password from a file (0600 in
// the keys folder, removed afterwards), never argv, which every user of the
// machine can read.
func seedAdminCLI(user, pass string) error {
	dir := config.KeysDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".admin-password-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(pass + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return runAPISub("adduser", []string{"--password-file", f.Name(), user})
}

func splitRoots(line string) []string {
	var out []string
	for _, r := range strings.Split(line, ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// generateToken mints a URL-safe 32-byte admin token.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is catastrophic; surface rather than emit
		// a predictable token.
		fatalf("generate admin token: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
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
