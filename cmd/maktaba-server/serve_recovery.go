package main

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logkeeper"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/recovery"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// serveRecovery is the recovery console (Epic 53 story 53.04) once the supervisor exists;
// before that /recovery says to wait (nothing can be shown yet).
var serveRecovery atomic.Pointer[recovery.Handler]

// serveRecoveryHTTP is /recovery on the web port.
func serveRecoveryHTTP(w http.ResponseWriter, r *http.Request) {
	if h := serveRecovery.Load(); h != nil {
		h.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Retry-After", "5")
	http.Error(w, "Maktaba is starting", http.StatusServiceUnavailable)
}

// recoveryDeps is what the console needs of serve.
type recoveryDeps struct {
	cfg        config.Config
	sup        *supervisor.Supervisor
	rec        *supervisor.Reconciler
	keeper     *logkeeper.Keeper
	upd        func() *updates
	releaseDir string
}

// startRecovery builds the console and returns the tray's link handler for the admin port.
func startRecovery(d recoveryDeps) http.Handler {
	state := d.cfg.StateDir()
	codes := &recovery.Codes{}
	cat := components.Default()
	var actionsMu sync.Mutex
	record := func(who recovery.Who, action, detail string) {
		by := "tray"
		if who.UserID != "" {
			by = mlog.HashUser(who.UserID)
		}
		d.sup.Note(supervisor.Event{Kind: "recovery_action", Reason: action, Message: by + " " + detail})
		if d.keeper != nil {
			line, _ := json.Marshal(map[string]any{"level": "warn", "msg": "recovery_action", "action": action,
				"detail": detail, "by": by})
			d.keeper.Ingest(logkeeper.Origin{Service: "supervisor"}, line)
		}
		// for audit_log, which the API fills from this file when it next starts
		row, _ := json.Marshal(map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "action": action,
			"detail": detail, "user_id": who.UserID, "tray": who.Tray})
		actionsMu.Lock()
		defer actionsMu.Unlock()
		p := supervisor.RecoveryActionsPath(state)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err == nil {
			if f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600); err == nil {
				_, _ = f.Write(append(row, '\n'))
				_ = f.Close()
			}
		}
	}
	h := &recovery.Handler{
		Verifier: &recovery.Verifier{PublicKeys: recoveryKeys, RevokedPath: supervisor.RecoveryRevokedPath(state)},
		Codes:    codes,
		Sessions: &recovery.Sessions{},
		Lockout:  &recovery.Lockout{},
		Status: func() supervisor.StatusDoc {
			if b := serveStatus.Load(); b != nil {
				return b.Doc()
			}
			return supervisor.StartingStatus(time.Now())
		},
		Processes: func() []supervisor.ChildStatus { return d.sup.Snapshot().Processes },
		Catalog:   cat,
		Keeper:    d.keeper,
		Record:    record,
		Secure:    strings.HasPrefix(strings.ToLower(d.cfg.PublicStreamURL()), "https://"),
		Disk: func() (int64, int64) {
			root := LogsRoot(d.cfg)
			return logkeeper.FreeBytes(root), dirSize(filepath.Join(root, "segments"))
		},
		Diagnostics: func(add func(string, []byte)) {
			for name, p := range map[string]string{
				"supervisor-events.jsonl": filepath.Join(d.cfg.Server.DataDir, "supervisor", "events.jsonl"),
				"components.json":         supervisor.CompCachePath(state),
			} {
				if b, err := readTail(p, 4<<20); err == nil {
					add(name, []byte(mlog.Scrub(string(b))))
				}
			}
			snap, _ := json.MarshalIndent(d.sup.Snapshot(), "", "  ")
			add("processes.json", []byte(mlog.Scrub(string(snap))))
		},
	}
	if d.rec != nil {
		h.Phases = d.rec.Phases
	}
	h.Actions = recovery.Actions{
		RestartComponent: d.sup.RestartComponent,
		RestartAll: func() {
			for _, id := range cat.Order() {
				d.sup.RestartComponent(id)
			}
		},
		SafeMode: func(on bool) error {
			if on {
				if err := supervisor.RequestSafeMode(state, time.Now()); err != nil {
					return err
				}
			} else if err := supervisor.CancelSafeMode(state); err != nil {
				return err
			}
			return requestServeRestart(d.releaseDir)
		},
		Rollback: func() (string, func() error) {
			u := d.upd()
			if u == nil || u.m == nil {
				return "", nil
			}
			j := u.m.Status().Journal
			if j == nil || j.State != updater.StateCommitted || j.From == "" {
				return "", nil
			}
			run := j.Job.RunID
			return j.From, func() error { return u.m.RollbackCommitted(run) }
		},
		Backups: func() ([]recovery.Backup, error) {
			u := d.upd()
			if u == nil || u.r == nil {
				return nil, nil
			}
			var out []recovery.Backup
			for _, b := range u.r.list().Backups {
				out = append(out, recovery.Backup{Name: b.Name, At: b.At, Size: b.Bytes})
			}
			return out, nil
		},
		Restore: func(name, by string) error {
			u := d.upd()
			if u == nil || u.r == nil {
				return errors.New("this machine restores no backups (a joined machine's database is the home's)")
			}
			_, err := u.r.restore(context.Background(), name, by, true, false)
			return err
		},
	}
	serveRecovery.Store(h)
	return recovery.LinkHandler(codes, "http://"+supervisor.LocalAddr(d.cfg.Server.WebListen, "8088"), record)
}

// recoveryKeys are the API's signing keys' public halves: the operator's
// MAKTABA_JWT_PUBLIC_KEY_PEM, else the pair the supervisor keeps (and gives the API).
func recoveryKeys() []*rsa.PublicKey {
	if pem := os.Getenv("MAKTABA_JWT_PUBLIC_KEY_PEM"); strings.TrimSpace(pem) != "" {
		return recovery.PublicKeysFrom([]byte(pem))
	}
	_, pub := supervisor.JWTKeyPaths()
	b, err := os.ReadFile(pub)
	if err != nil {
		return nil
	}
	return recovery.PublicKeysFrom(b)
}

// requestServeRestart asks the launcher to start serve again (its updates/restart file,
// as a settings restart does); with no launcher there is no one to start it again.
func requestServeRestart(releaseDir string) error {
	l := restartLayout(releaseDir)
	if l == nil {
		return errors.New("no launcher runs this server: restart Maktaba to apply it")
	}
	p := l.RestartRequestPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(time.Now().UTC().Format(time.RFC3339)+" recovery console\n"), 0o600)
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

// readTail is a file's last max bytes.
func readTail(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := int64(0)
	if fi.Size() > limit {
		off = fi.Size() - limit
	}
	buf := make([]byte, fi.Size()-off)
	_, err = f.ReadAt(buf, off)
	if err != nil && fi.Size()-off > 0 && len(buf) == 0 {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	return buf, nil
}

// recoveryAdmin adds the tray's link (POST /recovery/link) to the supervisor's loopback
// admin port.
func recoveryAdmin(base, link http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/recovery/link" {
			link.ServeHTTP(w, r)
			return
		}
		base.ServeHTTP(w, r)
	})
}
