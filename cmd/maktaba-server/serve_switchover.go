package main

// Switching the home (specs/epics/37-node-replica, "Switching the home"; the database
// steps are internal/switchover), as this machine's supervisor runs its part.
//
// On the home its API runs the switch and asks the supervisor, over its loopback
// admin with the per-boot local token, for what only the supervisor may do: freeze the
// database, gather and seal the kit for the machine taking over, and at the end make
// this machine a machine of the new home. On the machine taking over, and on every
// other machine, the home's commands arrive over the machine agent's channel: whether
// the copy holds the switch's marker, the kit's chunks, the promotion, the new home's
// address. A machine whose part changes starts again in its new one (serve reloads its
// configuration and node.json).

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/curve25519"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/nodecopy"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/switchover"
)

// errReload: serve's configuration changed (this machine's part in the cluster): it
// starts again.
var errReload = errors.New("serve: this machine's configuration changed")

var reloadCh = make(chan struct{}, 1)

// requestReload asks serve to stop its processes and start again with its
// configuration read anew.
func requestReload() {
	select {
	case reloadCh <- struct{}{}:
	default:
	}
}

// Key files the kit carries, by name.
const (
	kitSecretsKey = "secrets.key"
	kitClusterKey = "home-cluster.key"
	kitCACert     = pgbundle.CACertFile
	kitCAKey      = "postgres-ca.key"
	kitHFToken    = "hf-token"
)

// switching is this machine's part of a switch.
type switching struct {
	cfg config.Config
	// pg is the home's bundled Postgres (nil on a node), cp a node's copy (nil on the
	// home), stopCopy stops the copy's manager (the copy's database runs on).
	pg       *pgbundle.Bundle
	cp       *nodeCopy
	stopCopy func()
	token    string
	// ready is closed once the above are known (a command waits for it)
	ready chan struct{}

	mu   sync.Mutex
	busy string // the switchover under way here
}

func newSwitching(cfg config.Config) *switching {
	return &switching{cfg: cfg, ready: make(chan struct{})}
}

// set records this machine's databases (serve resolves them after the machine agent
// starts).
func (s *switching) set(pg *pgbundle.Bundle, cp *nodeCopy, stopCopy func(), token string) {
	s.pg, s.cp, s.stopCopy, s.token = pg, cp, stopCopy, token
	close(s.ready)
}

func (s *switching) dir(id string) string {
	return filepath.Join(s.cfg.StateDir(), "switchover", filepath.Base(id))
}

func superDB(b pgbundle.Bundle) (*sql.DB, error) {
	dsn, err := b.SuperURL()
	if err != nil {
		return nil, err
	}
	return sql.Open("postgres", dsn)
}

// clusterKeyPath is the home's cluster key (the API's agent.ClusterKeyPath).
func clusterKeyPath() string {
	if p := os.Getenv("MAKTABA_HOME_CLUSTER_KEY"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".maktaba", "cluster", "home-cluster.key")
	}
	return filepath.Join(home, ".maktaba", "cluster", "home-cluster.key")
}

func secretsKeyPath() string {
	if p := os.Getenv("MAKTABA_SECRETS_KEY"); p != "" {
		return p
	}
	return supervisor.SecretsKeyPath()
}

func hfTokenPath(cfg config.Config) string {
	if p := os.Getenv("HF_HOME"); p != "" {
		return filepath.Join(p, "token")
	}
	return filepath.Join(cfg.StateDir(), "models", "huggingface", "token")
}

// kitFiles are the home's key files the new home needs (the ones this home has).
func (s *switching) kitFiles() map[string]string {
	out := map[string]string{kitSecretsKey: secretsKeyPath(), kitClusterKey: clusterKeyPath(),
		kitHFToken: hfTokenPath(s.cfg)}
	if s.pg != nil {
		out[kitCACert] = s.pg.CACert()
		out[kitCAKey] = filepath.Join(s.pg.Keys, kitCAKey)
	} else {
		out[kitCACert] = filepath.Join(config.KeysDir(), kitCACert)
		out[kitCAKey] = filepath.Join(config.KeysDir(), kitCAKey)
	}
	return out
}

// ---------- the home's side: its API's loopback calls ----------

// adminHandler adds POST /switchover/{freeze,thaw,kit,demote} to the supervisor's
// loopback admin, for a caller holding the per-boot local token.
func (s *switching) adminHandler(base http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("/switchover/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || s.token == "" ||
			r.Header.Get("Authorization") != "Bearer "+s.token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if s.pg == nil {
			http.Error(w, "this home's database is not Maktaba's own Postgres: it cannot be switched", http.StatusConflict)
			return
		}
		var req homeRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		var out any
		var err error
		switch strings.TrimPrefix(r.URL.Path, "/switchover/") {
		case "check":
			// this home can be switched: its database is Maktaba's own (above)
			out = map[string]any{"ok": true}
		case "freeze":
			err = s.withSuper(func(db *sql.DB) error { return switchover.Freeze(ctx, db, req.ID) })
		case "thaw":
			err = s.withSuper(func(db *sql.DB) error { return switchover.Thaw(ctx, db) })
		case "kit":
			out, err = s.kit(ctx, req)
		case "demote":
			// answered first: the demotion ends with this machine starting again, the API
			// that asked among its processes
			if err = s.claim(req.ID); err == nil {
				go s.demote(req)
				out = map[string]any{"accepted": true}
			}
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	return mux
}

func (s *switching) claim(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy != "" && s.busy != id {
		return fmt.Errorf("another switchover (%s) is under way", s.busy)
	}
	s.busy = id
	return nil
}

func (s *switching) withSuper(fn func(*sql.DB) error) error {
	db, err := superDB(*s.pg)
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(db)
}

// homeRequest is the API's call: the switchover, its term, the machine taking over
// (its id and public key), and for the demotion the new home's addresses.
type homeRequest struct {
	ID       string `json:"id"`
	Term     int64  `json:"term"`
	ToNode   string `json:"to_node"`
	ToPubKey string `json:"to_pubkey"`
	// the new home, for the demotion
	Home newHome `json:"home"`
}

// newHome is where a machine finds its new home.
type newHome struct {
	APIURL     string `json:"api_url"`
	WebURL     string `json:"web_url"`
	DBHost     string `json:"db_host"`
	DBPort     int    `json:"db_port"`
	SystemID   string `json:"system_id"`
	InstanceID string `json:"instance_id"`
	Name       string `json:"name"`
}

// oldHome is this machine's identity as a machine of the new home, chosen when the kit
// is made (it travels sealed in the kit) and kept here for the demotion.
type oldHome struct {
	NodeID       string `json:"node_id"`
	PrivateKey   string `json:"private_key"`
	PublicKey    string `json:"public_key"`
	RolePassword string `json:"role_password"`
	Name         string `json:"name"`
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// kit gathers the kit on this (frozen) home and seals it for the machine taking over:
// <state>/switchover/<id>/kit.chunks, which the API sends over the channel.
func (s *switching) kit(ctx context.Context, req homeRequest) (any, error) {
	if err := s.claim(req.ID); err != nil {
		return nil, err
	}
	to, err := base64.StdEncoding.DecodeString(req.ToPubKey)
	if err != nil || len(to) != 32 {
		return nil, errors.New("the machine's public key")
	}
	clusterPriv, err := os.ReadFile(clusterKeyPath())
	if err != nil || len(clusterPriv) != 32 {
		return nil, fmt.Errorf("this home's cluster key: %v", err)
	}
	dir := s.dir(req.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// this machine, as a machine of the new home
	me, err := s.oldHome(dir)
	if err != nil {
		return nil, err
	}
	copyPW, err := nodecopy.Copy{NodeID: me.NodeID, DB: *s.pg}.Password()
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for name, path := range s.kitFiles() {
		if b, err := os.ReadFile(path); err == nil {
			files[name] = b
		}
	}
	if len(files[kitSecretsKey]) == 0 {
		return nil, errors.New("this home has no secrets key to hand over")
	}
	var k *switchover.Kit
	err = s.withSuper(func(db *sql.DB) error {
		var err error
		k, err = switchover.Gather(ctx, db, req.ID, req.Term, files)
		return err
	})
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	k.OldHome = &switchover.Machine{ID: me.NodeID, Name: me.Name, OS: runtime.GOOS, Arch: runtime.GOARCH,
		PublicKey: me.PublicKey, RolePassword: me.RolePassword, CopyPassword: copyPW, WebURL: s.cfg.PublicWebURL()}
	if k.OldHome.Name == "" {
		k.OldHome.Name = host
	}
	key, err := switchover.KitKey(clusterPriv, to, req.ID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "kit.chunks")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	n := 0
	sealer := switchover.NewSealer(key, req.ID, func(i uint64, last bool, sealed []byte) error {
		n++
		return switchover.WriteChunk(f, i, last, sealed)
	})
	werr := k.Write(sealer)
	if werr == nil {
		werr = sealer.Close()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, werr
	}
	return map[string]any{"path": path, "chunks": n, "old_home": me.NodeID}, nil
}

// oldHome is this machine's identity as a machine of the new home (made once per
// switchover).
func (s *switching) oldHome(dir string) (oldHome, error) {
	path := filepath.Join(dir, "old-home.json")
	var me oldHome
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &me) == nil && me.NodeID != "" {
		return me, nil
	}
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return me, err
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return me, err
	}
	pw, err := randomSecret()
	if err != nil {
		return me, err
	}
	host, _ := os.Hostname()
	me = oldHome{NodeID: uuid.NewString(), PrivateKey: base64.StdEncoding.EncodeToString(priv),
		PublicKey: base64.StdEncoding.EncodeToString(pub), RolePassword: pw, Name: host}
	b, _ := json.Marshal(me)
	return me, writePrivate(path, b)
}

func writePrivate(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// demote makes this home a machine of the new one: its database a copy of the new
// home's without copying again (it holds the marker), its node.json written, its home
// role given up; then it starts again as a machine.
func (s *switching) demote(req homeRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, "[switchover] "+format+"\n", args...) }
	me, err := s.oldHome(s.dir(req.ID))
	if err != nil {
		logf("this machine's identity: %v", err)
		return
	}
	if err := s.withSuper(func(db *sql.DB) error { return switchover.Demote(ctx, db) }); err != nil {
		logf("demote: %v", err)
		return
	}
	role := switchover.Machine{ID: me.NodeID}.Role()
	h := req.Home
	dsn := (&url.URL{Scheme: "postgres", User: url.UserPassword(role, me.RolePassword),
		Host: net.JoinHostPort(h.DBHost, strconv.Itoa(h.DBPort)), Path: "/" + pgbundle.AppDatabase,
		RawQuery: "sslmode=prefer&connect_timeout=10"}).String()
	c := nodecopy.Copy{NodeID: me.NodeID, HomeDSN: dsn, DB: *s.pg}
	local, err := c.LocalSchema(ctx)
	if err != nil {
		logf("this machine's schema: %v", err)
		return
	}
	ca, _ := os.ReadFile(s.pg.CACert())
	pem := string(ca)
	slot := switchover.Machine{ID: me.NodeID}.Slot()
	g := nodecopy.Grant{Role: switchover.Machine{ID: me.NodeID}.CopyRole(), Slot: slot, SlotExists: true,
		Publication: slot, SystemID: h.SystemID, Database: pgbundle.AppDatabase, CA: &pem}
	if err := c.Handover(ctx, g, local); err != nil {
		logf("following the new home: %v", err)
		return
	}
	clusterPriv, err := os.ReadFile(clusterKeyPath())
	if err != nil {
		logf("the cluster key: %v", err)
		return
	}
	clusterPub, _ := curve25519.X25519(clusterPriv, curve25519.Basepoint)
	n := config.NodeConfig{NodeID: me.NodeID, Name: me.Name, PrivateKey: me.PrivateKey, DatabaseURL: dsn,
		Home: config.HomeRef{URL: h.APIURL, APIURL: h.APIURL, InstanceID: h.InstanceID, Name: h.Name,
			ClusterPub: base64.StdEncoding.EncodeToString(clusterPub)}}
	if err := writeNodeConfig(n); err != nil {
		logf("node.json: %v", err)
		return
	}
	// this machine's cluster key is the new home's now; it keeps no copy of it
	_ = os.Rename(clusterKeyPath(), clusterKeyPath()+".was-home-"+strconv.FormatInt(req.Term-1, 10))
	if err := editConfig(func(c *config.Config) {
		if strings.EqualFold(strings.TrimSpace(c.Server.Role), "home") {
			c.Server.Role = ""
		}
	}); err != nil {
		logf("server.toml: %v", err)
	}
	logf("this machine is a machine of the new home now (%s): starting again", h.APIURL)
	requestReload()
}

// editConfig changes server.toml as it is on disk (never what serve made of it: the
// bundled Postgres's address, the ports it resolved), keeping a database url unset.
func editConfig(fn func(*config.Config)) error {
	c, _, err := config.Load()
	if err != nil {
		return err
	}
	if !c.DatabaseURLSet {
		c.Database.URL = ""
	}
	fn(&c)
	return c.Save()
}

func writeNodeConfig(n config.NodeConfig) error {
	b, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(config.LocalNodeConfigPath(), b)
}

// ---------- a machine's side: the home's commands over the channel ----------

// command answers the switchover's commands (machineagent.Command).
func (s *switching) command(ctx context.Context, name string, args json.RawMessage) (handled, ok bool, detail any) {
	var a struct {
		ID   string  `json:"id"`
		Term int64   `json:"term"`
		I    uint64  `json:"i"`
		Last bool    `json:"last"`
		Data string  `json:"data"`
		Home newHome `json:"home"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.ID == "" {
		return strings.HasPrefix(name, "switch.") || name == "home.moved", false, "bad arguments"
	}
	select {
	case <-s.ready:
	case <-time.After(time.Minute):
		return true, false, "this machine is still starting"
	}
	var out any
	var err error
	switch name {
	case "switch.prepare":
		out, err = s.prepare(ctx, a.ID)
	case "switch.kit":
		err = s.chunk(a.ID, a.I, a.Last, a.Data)
	case "switch.promote":
		out, err = s.promote(ctx, a.ID, a.Term)
	case "home.moved":
		out, err = s.follow(ctx, a.ID, a.Home)
	default:
		return false, false, nil
	}
	if err != nil {
		return true, false, err.Error()
	}
	return true, true, out
}

func (s *switching) copyDB() (*sql.DB, error) {
	if s.cp == nil {
		return nil, errors.New("this machine keeps no copy of its home's database")
	}
	return superDB(s.cp.copy.DB)
}

// prepare reports whether this machine's copy holds the switch's marker (it waits for
// it a minute: the copy may be a moment behind).
func (s *switching) prepare(ctx context.Context, id string) (any, error) {
	db, err := s.copyDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	end := time.Now().Add(time.Minute)
	for {
		ok, err := switchover.HasMarker(ctx, db, id)
		if ok || time.Now().After(end) || ctx.Err() != nil {
			local, _ := s.cp.copy.LocalSchema(ctx)
			return map[string]any{"marker": ok, "local_schema": local, "error": errString(err)}, nil
		}
		time.Sleep(time.Second)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// chunk keeps one sealed chunk of the kit.
func (s *switching) chunk(id string, i uint64, last bool, data string) error {
	sealed, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return err
	}
	dir := s.dir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if i == 0 {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(filepath.Join(dir, "kit.chunks"), flag, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return switchover.WriteChunk(f, i, last, sealed)
}

// openKit opens the kit the home sealed for this machine.
func (s *switching) openKit(id string, n config.NodeConfig) (*switchover.Kit, error) {
	priv, err := base64.StdEncoding.DecodeString(n.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("this machine's key: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(n.Home.ClusterPub)
	if err != nil {
		return nil, fmt.Errorf("the home's cluster key: %w", err)
	}
	key, err := switchover.KitKey(priv, pub, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(s.dir(id), "kit.chunks"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var plain bytes.Buffer
	o := switchover.NewOpener(key, id, &plain)
	if err := switchover.ReadChunks(f, o.Chunk); err != nil {
		return nil, err
	}
	if !o.Done() {
		return nil, switchover.ErrIncomplete
	}
	return switchover.ReadKit(&plain)
}

// promote makes this machine the home: its copy promoted with the kit, the home's key
// files in place, its Postgres the bundled home's (on the LAN, TLS from the cluster's
// CA), node.json moved aside; answered with its addresses, then it starts again as the
// home.
func (s *switching) promote(ctx context.Context, id string, term int64) (any, error) {
	if err := s.claim(id); err != nil {
		return nil, err
	}
	n := supervisor.NodeConfig()
	kit, err := s.openKit(id, n)
	if err != nil {
		return nil, fmt.Errorf("the kit: %w", err)
	}
	if kit.Switchover != id || kit.Term != term {
		return nil, errors.New("the kit is another switchover's")
	}
	db, err := s.copyDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if s.stopCopy != nil {
		s.stopCopy()
	}
	done, err := switchover.Promote(ctx, db, switchover.Promotion{Self: n.NodeID, Name: n.Name, Term: term,
		Reason: "planned", Kit: kit, OldHome: kit.OldHome})
	if err != nil {
		return nil, err
	}
	// the home's key files
	for name, path := range s.kitFiles() {
		if b, ok := kit.Files[name]; ok {
			if err := writePrivate(path, b); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	// this machine's Postgres is the home's from its next start: the bundle where the copy
	// is, on the LAN with TLS, and no database url of its own
	db2 := s.cp.copy.DB
	if err := editConfig(func(c *config.Config) {
		c.Postgres.Enabled, c.Postgres.Home, c.Postgres.Port, c.Postgres.CopyPort = "on", db2.Home, db2.Port, 0
		c.Database.URL = ""
	}); err != nil {
		return nil, fmt.Errorf("server.toml: %w", err)
	}
	path := config.LocalNodeConfigPath()
	if err := os.Rename(path, path+".was-node-"+strconv.FormatInt(term-1, 10)); err != nil {
		return nil, fmt.Errorf("node.json: %w", err)
	}
	home := newHome{APIURL: s.cfg.PublicAPIURL(), WebURL: s.cfg.PublicWebURL(), DBPort: db2.Port,
		SystemID: done.SystemID, InstanceID: n.Home.InstanceID, Name: n.Name}
	if u, err := url.Parse(home.APIURL); err == nil {
		home.DBHost = u.Hostname()
	}
	fmt.Fprintf(os.Stderr, "[switchover] this machine is the home now (term %d): starting again\n", term)
	go func() {
		time.Sleep(2 * time.Second) // the answer goes out first
		requestReload()
	}()
	return home, nil
}

// follow moves this machine to the new home: its copy without copying again when it
// holds the switch's marker (else it copies anew once it starts again), node.json
// pointed at the new home; then it starts again.
func (s *switching) follow(ctx context.Context, id string, h newHome) (any, error) {
	n := supervisor.NodeConfig()
	u, err := url.Parse(n.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("node.json's database: %w", err)
	}
	u.Host = net.JoinHostPort(h.DBHost, strconv.Itoa(h.DBPort))
	dsn := u.String()
	moved := false
	if s.cp != nil {
		if db, err := s.copyDB(); err == nil {
			ok, _ := switchover.HasMarker(ctx, db, id)
			_ = db.Close()
			if ok {
				if s.stopCopy != nil {
					s.stopCopy()
				}
				c := s.cp.copy
				c.HomeDSN = dsn
				local, err := c.LocalSchema(ctx)
				if err == nil {
					slot := c.Slot()
					err = c.Handover(ctx, nodecopy.Grant{Role: c.Role(), Slot: slot, SlotExists: true, Publication: slot,
						SystemID: h.SystemID, Database: pgbundle.AppDatabase}, local)
				}
				if err != nil {
					return nil, fmt.Errorf("following the new home: %w", err)
				}
				moved = true
			}
		}
	}
	n.DatabaseURL = dsn
	n.Home.URL, n.Home.APIURL, n.Home.Name = h.APIURL, h.APIURL, h.Name
	if err := writeNodeConfig(n); err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "[switchover] the home is %s now (copy kept: %v): starting again\n", h.APIURL, moved)
	go func() {
		time.Sleep(2 * time.Second)
		requestReload()
	}()
	return map[string]any{"copy_kept": moved}, nil
}

