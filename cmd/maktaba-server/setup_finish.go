package main

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// setupDatabase runs fn against this install's database: the bundled
// Postgres with a short-lived server of its own (prepared first: initdb and
// the app role on a first run), or a Postgres [database] url. Tests replace it.
var setupDatabase = func(s *setupRun, fn func(dbURL string) error) error {
	exe, _ := os.Executable()
	b, ok, err := pgbundle.Resolve(s.cfg, exe)
	if err != nil {
		return err
	}
	if ok {
		s.progress("database", "Creating the library database", 0, 0)
		if err := b.Prepare(s.ctx, s.log); err != nil {
			return failStep("database_failed", "", "The library database could not be created: %v", err)
		}
		url, err := b.URL()
		if err != nil {
			return err
		}
		return b.WithServer(s.ctx, s.log, func() error { return fn(url) })
	}
	if u := s.cfg.Database.URL; strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://") {
		return fn(u)
	}
	return failStep("database_unsupported", "", "Setup needs the bundled Postgres or a Postgres [database] url (this install uses %s).",
		strings.SplitN(s.cfg.Database.URL, ":", 2)[0])
}

// stepFinish applies what the steps collected: server.toml, the database
// (created, migrated), the admin account (seeded from the password's hash),
// and the Plex server when the user signed in. Then the app registers the
// login item, which starts the server.
func stepFinish(s *setupRun, _ json.RawMessage) (any, error) {
	p, err := s.loadPending()
	if err != nil {
		return nil, err
	}
	if p.Admin == nil || p.Admin.PasswordHash == "" {
		return nil, failStep("admin_missing", "", "Create the admin account first (the admin step).")
	}

	cfg := s.cfg
	if len(p.Roots) > 0 {
		cfg.Media.Roots = p.Roots
	}
	if p.PathMap != "" {
		cfg.Media.PathMap = p.PathMap
	}
	if err := ensureDataDir(cfg.Server.DataDir); err != nil {
		return nil, err
	}
	// the web, API and streaming ports another program holds move into the
	// fallback range before the config is written (config/ports.go)
	if _, err := reconcilePorts(&cfg, config.Path(), config.PortOptions{KeepOurs: true}, s.log, false); err != nil {
		return nil, err
	}
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	s.cfg = cfg
	result := map[string]any{
		"web_url":        "http://" + supervisor.LocalAddr(cfg.Server.WebListen, "8088"),
		"supervisor_url": supervisor.StateURL(cfg),
	}

	err = setupDatabase(s, func(dbURL string) error {
		s.progress("migrate", "Preparing the library database", 0, 0)
		if _, code, err := s.apiSub(dbURL, nil, "migrate", "up"); err != nil {
			return failStep("migrate_failed", "", "The database migrations failed (exit %d); the details are in the log.", code)
		}
		s.progress("admin", "Creating your admin account", 0, 0)
		if err := s.seedAdmin(dbURL, *p.Admin); err != nil {
			return err
		}
		if p.Plex != nil && p.Plex.Linked {
			s.progress("plex", "Connecting your Plex server", 0, 0)
			result["plex"] = s.linkPlex(dbURL, p.Plex.ClientID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// done: nothing waits any more (the password's hash included)
	if err := os.Remove(s.pendingPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return result, nil
}

// seedAdmin creates the admin account from the hash the admin step saved. The
// hash goes to `maktaba-api adduser` through a 0600 file in the keys folder,
// removed afterwards. An admin already there (a finish run again after it
// seeded one: exit 3) is not an error.
func (s *setupRun) seedAdmin(dbURL string, a pendingAdmin) error {
	dir := config.KeysDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".admin-hash-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(a.PasswordHash + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_, code, err := s.apiSub(dbURL, nil, "adduser", "--password-hash-file", f.Name(), a.Username)
	if err != nil && code != 3 {
		return failStep("admin_failed", "", "The admin account could not be created (exit %d); the details are in the log.", code)
	}
	return nil
}

// linkPlex stores the Plex server the signed-in account reaches
// (`maktaba-api plex-link`) and deletes the account token. Plex is optional:
// a failure is reported in the result and setup goes on (sign in again from
// Settings › Extensions › Plex).
func (s *setupRun) linkPlex(dbURL, clientID string) map[string]any {
	tok := plexTokenPath()
	defer func() { _ = os.Remove(tok) }()
	if _, err := os.Stat(tok); err != nil {
		return map[string]any{"linked": false, "error": "the Plex sign-in was not finished"}
	}
	out, code, err := s.apiSub(dbURL, nil, "plex-link", "--token-file", tok, "--client-id", clientID)
	if err != nil {
		return map[string]any{"linked": false, "error": "the Plex server could not be stored (exit " + strconv.Itoa(code) + "); sign in again in Settings"}
	}
	var got struct {
		Server  string   `json:"server"`
		URL     string   `json:"url"`
		Servers []string `json:"servers"`
	}
	_ = json.Unmarshal(out, &got)
	return map[string]any{"linked": true, "server": got.Server, "url": got.URL, "servers": got.Servers}
}
