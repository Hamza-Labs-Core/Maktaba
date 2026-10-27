package main

// The server's side of where it keeps its data (Epic 36 story 36.15):
//
//   - before a whole machine's serve starts anything, storageGate waits while the
//     disk that holds the data folder the owner chose is not there (an external disk
//     not mounted yet) and never makes a new empty database in its place, then does
//     the move the web asked for (Settings → Storage), with every process stopped.
//     Meanwhile the supervisor's admin answers /processes with what it waits on
//     (storage_waiting, storage_moving: detail_codes.json), so the desktop app and
//     the start screens say it;
//   - /config/storage on the supervisor's admin is what the web's page reads and
//     asks (the API proxies it for its admins).

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/storage"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// storagePoll is how often a start waiting for its disk looks again.
var storagePoll = 3 * time.Second

// holdingAdmin answers the supervisor's /processes while serve runs no process yet:
// one row, starting, with what it waits on.
type holdingAdmin struct {
	mu     sync.Mutex
	detail supervisor.Detail
	srv    *http.Server
}

func (h *holdingAdmin) set(d supervisor.Detail) {
	h.mu.Lock()
	h.detail = d
	h.mu.Unlock()
}

func (h *holdingAdmin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/processes" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	h.mu.Lock()
	d := h.detail
	h.mu.Unlock()
	host, _ := os.Hostname()
	snap := supervisor.Snapshot{Host: host, StartedAt: time.Now().UTC(), Processes: []supervisor.ChildStatus{{
		Role: supervisor.RoleAPI, State: supervisor.StateStarting, Detail: d.Text, DetailCode: d.Code,
		DetailParams: d.Params}}, Events: []supervisor.Event{}}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snap)
}

// startHolding serves h on the supervisor's admin address of cfg (nil when it has none).
func startHolding(cfg config.Config, d supervisor.Detail) *holdingAdmin {
	h := &holdingAdmin{detail: d}
	if p, err := supervisor.PolicyFromConfig(cfg.Supervisor); err == nil && p.AdminAddr != "" {
		h.srv = startSupervisorAdmin(p.AdminAddr, h)
	}
	return h
}

func (h *holdingAdmin) stop() {
	if h != nil && h.srv != nil {
		_ = shutdownWeb(h.srv)
	}
}

// wholeMachineRole: serve runs the whole machine (no --role, or all).
func wholeMachineRole(role string) bool { return role == "" || role == "all" }

// storageGate runs before a whole machine's serve: the move the web asked for,
// then the wait for a data folder whose disk is not there. It returns the config
// read again (a move changed data_dir).
func storageGate(cfg config.Config, role string, log io.Writer) (config.Config, error) {
	if !wholeMachineRole(role) {
		if path, missing := cfg.StorageMissing(); missing {
			return cfg, fmt.Errorf("the disk that holds Maktaba's data (%s) is not connected", path)
		}
		return cfg, nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var hold *holdingAdmin
	defer func() { hold.stop() }()
	show := func(d supervisor.Detail) {
		if hold == nil {
			hold = startHolding(cfg, d)
			return
		}
		hold.set(d)
	}
	if req, ok := storage.ReadRequest(config.TOMLDir()); ok {
		if err := requestedMove(ctx, cfg, req, show, log); err != nil {
			fmt.Fprintf(log, "[storage] level=error the move to %s did not happen: %v\n", req.Target, err)
		}
		var err error
		if cfg, _, err = config.Load(); err != nil {
			return cfg, err
		}
	}
	said := false
	for {
		path, missing := cfg.StorageMissing()
		if !missing {
			if said {
				fmt.Fprintf(log, "[storage] the disk of %s is here: starting\n", cfg.Server.DataDir)
			}
			return cfg, nil
		}
		if !said {
			said = true
			fmt.Fprintf(log, "[storage] level=warn Maktaba's data is on a disk that is not connected (%s): waiting "+
				"for it. Nothing starts until it is there, and no new database is made in its place\n", path)
		}
		show(detail("storage_waiting", fmt.Sprintf("Waiting for the disk that holds Maktaba's data (%s)", path), path))
		select {
		case <-ctx.Done():
			return cfg, fmt.Errorf("stopped while waiting for the disk of %s", path)
		case <-time.After(storagePoll):
		}
		next, _, err := config.Load()
		if err == nil {
			cfg = next
		}
	}
}

// requestedMove does the move the web asked for, every process stopped; a refusal
// or a failure is recorded for the page (storage.ResultFile), and the install
// runs from where it was.
func requestedMove(ctx context.Context, cfg config.Config, req storage.Request, show func(supervisor.Detail),
	log io.Writer) error {
	in := storageInstall(cfg)
	configDir := config.TOMLDir()
	fail := func(err error) error {
		storage.WriteFailure(configDir, req.Target, in.From, err)
		_ = storage.DropRequest(configDir)
		show(detail("storage_move_failed", fmt.Sprintf("Could not move Maktaba's data to %s: %v", req.Target, err),
			req.Target, err.Error()))
		return err
	}
	// the request may predate a move the command line finished (data_dir is the target)
	if filepath.Clean(cfg.Server.DataDir) == filepath.Clean(req.Target) {
		if _, _, ok := storage.ReadJournal(configDir); !ok {
			return storage.DropRequest(configDir)
		}
	}
	if reason := cannotMove(cfg); reason != "" {
		return fail(errors.New(reason))
	}
	fmt.Fprintf(log, "[storage] moving Maktaba's data to %s (asked by %s)\n", req.Target, orUnknown(req.By))
	show(detail("storage_moving", "Moving Maktaba's data to "+req.Target, req.Target, 0, 0, 0))
	_, err := storage.Move(ctx, in, req.Target, storage.Options{
		Log: log,
		By:  "move",
		Busy: func(from storage.Layout) error {
			if (pgbundleAt(from)).Running() {
				return errors.New("the library database still runs")
			}
			return nil
		},
		VerifyPostgres: verifyPostgresFor(cfg, log),
		Progress: func(done, total uint64) {
			pct := percentOf(done, total)
			show(detail("storage_moving", fmt.Sprintf("Moving Maktaba's data to %s (%d%%)", req.Target, pct),
				req.Target, done, total, pct))
		},
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// stopped (the machine shutting down): the next start finishes it
			return err
		}
		return fail(err)
	}
	return nil
}

// --- /config/storage on the supervisor's admin ----------------------------------

// storageRoutes serves the web's Storage page: GET /config/storage (where each
// folder is, its size, its disk), GET /config/storage/disks, POST
// /config/storage/check {target} and POST /config/storage/move {target} (202:
// the server stops every process, moves, and starts again).
func (c *configAdmin) storageRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/config/storage" && r.Method == http.MethodGet:
		cfg, _, err := config.LoadFrom(c.path)
		if err != nil {
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		writeConfigJSON(w, http.StatusOK, storageDoc(cfg))
	case r.URL.Path == "/config/storage/disks" && r.Method == http.MethodGet:
		cfg, _, _ := config.LoadFrom(c.path)
		writeConfigJSON(w, http.StatusOK, map[string]any{"disks": nonNil(storage.ListDisks(cfg.Server.DataDir,
			config.DefaultDataDir()))})
	case (r.URL.Path == "/config/storage/check" || r.URL.Path == "/config/storage/move") && r.Method == http.MethodPost:
		var body struct {
			Target string `json:"target"`
			By     string `json:"by"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
			writeConfigJSON(w, http.StatusBadRequest, map[string]string{"error": "the body: " + err.Error()})
			return true
		}
		cfg, _, err := config.LoadFrom(c.path)
		if err != nil {
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		plan := storage.PlanMove(storage.InstallOf(cfg, c.path), body.Target)
		if reason := cannotMove(cfg); reason != "" {
			plan.OK = false
			plan.Problems = append(plan.Problems, storage.Note{Code: "cannot_move", Message: reason})
		}
		if r.URL.Path == "/config/storage/check" {
			writeConfigJSON(w, http.StatusOK, plan)
			return true
		}
		if !plan.OK {
			writeConfigJSON(w, http.StatusUnprocessableEntity, plan)
			return true
		}
		if err := storage.WriteRequest(filepath.Dir(c.path), storage.Request{Target: plan.Target,
			By: strings.TrimSpace(body.By), At: time.Now().UTC()}); err != nil {
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		fmt.Fprintf(os.Stderr, "[serve] a move of Maktaba's data to %s was asked for from the web: starting again to do it\n",
			plan.Target)
		writeConfigJSON(w, http.StatusAccepted, map[string]string{"state": "requested", "target": plan.Target})
		c.reload()
	default:
		return false
	}
	return true
}

// pgbundleAt is the cluster in from's state root (its postmaster.pid says whether it runs).
func pgbundleAt(from storage.Layout) pgbundle.Bundle {
	return pgbundle.Bundle{Data: filepath.Join(from.Root, "postgres")}
}

func nonNil(d []storage.Disk) []storage.Disk {
	if d == nil {
		return []storage.Disk{}
	}
	return d
}

// authorized: the request carries the per-boot local token.
func (c *configAdmin) authorized(r *http.Request) bool {
	return c.token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.token)) == 1
}
