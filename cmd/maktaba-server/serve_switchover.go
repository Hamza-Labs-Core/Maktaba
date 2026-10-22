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
	"io"
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

// answerFirst is how long a step that starts this machine again waits first, so its
// answer reaches the API (tests make it short).
var answerFirst = 2 * time.Second

// Key files the kit carries, by name.
const (
	kitSecretsKey = "secrets.key"
	kitClusterKey = "home-cluster.key"
	kitCACert     = pgbundle.CACertFile
	kitCAKey      = "postgres-ca.key"
	kitHFToken    = "hf-token"
	kitJWTPrivate = "jwt-private.pem"
	kitJWTPublic  = "jwt-public.pem"
	// the cloud relay agent's credentials and key, and the home's [cloud] section
	kitRelayCred = supervisor.RelayCredFile
	kitRelayKey  = supervisor.RelayKeyFile
	kitCloud     = "cloud.json"
	// the model server the home's LLM stages used when it is another machine's ([llm] url)
	kitLLM = "llm.json"
)

// kitLLMServer is the [llm] url and models a kit carries.
type kitLLMServer struct {
	URL    string   `json:"url"`
	Models []string `json:"models,omitempty"`
}

// switching is this machine's part of a switch.
type switching struct {
	cfg config.Config
	// pg is the home's bundled Postgres (nil on a node), cp a node's copy (nil on the
	// home), stopCopy stops the copy's manager (the copy's database runs on).
	pg       *pgbundle.Bundle
	cp       *nodeCopy
	stopCopy func()
	token    string
	// want starts or stops one of this machine's processes (the supervisor's Want)
	want func(role supervisor.Role, on bool) error
	// ready is closed once the above are known (a command waits for it)
	ready chan struct{}

	mu   sync.Mutex
	busy string // the switchover under way here
	// promoting is the switchover whose promotion runs here now; demoting whether this
	// home's demotion runs, and how the last one ended
	promoting string
	demoting  bool
	demoteErr string
	demoted   string // the switchover whose demotion went through (this machine starts again)

	// claimMu keeps the emergencies' term claims one at a time (serve_termclaim.go)
	claimMu sync.Mutex
}

func newSwitching(cfg config.Config) *switching {
	return &switching{cfg: cfg, ready: make(chan struct{})}
}

// set records this machine's databases (serve resolves them after the machine agent
// starts).
func (s *switching) set(pg *pgbundle.Bundle, cp *nodeCopy, stopCopy func(), token string,
	want func(role supervisor.Role, on bool) error) {
	s.pg, s.cp, s.stopCopy, s.token, s.want = pg, cp, stopCopy, token, want
	s.sweepKits()
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
	priv, pub := supervisor.JWTKeyPaths()
	out := map[string]string{kitSecretsKey: secretsKeyPath(), kitClusterKey: clusterKeyPath(),
		kitHFToken: hfTokenPath(s.cfg), kitJWTPrivate: priv, kitJWTPublic: pub,
		kitRelayCred: supervisor.RelayCredPath(), kitRelayKey: supervisor.RelayKeyPath()}
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

// adminHandler adds POST /switchover/{check,freeze,thaw,kit,relay,demote} to the
// supervisor's loopback admin, for a caller holding the per-boot local token.
func (s *switching) adminHandler(base http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("/switchover/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || s.token == "" ||
			r.Header.Get("Authorization") != "Bearer "+s.token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		step := strings.TrimPrefix(r.URL.Path, "/switchover/")
		switch {
		case step == "emergency-status" || step == "emergency":
			// a standby home's own (its API asks for the admin there)
			if s.cp == nil {
				http.Error(w, "this machine keeps no copy of its home's database", http.StatusConflict)
				return
			}
		case step == "record" || step == "recorded" || step == "term-claim":
			// the API's own records: kept on any home (one that cannot be switched has none);
			// another standby's claim on a term: answered by every machine and the home
		case s.pg == nil:
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
		switch step {
		case "check":
			// this home can be switched: its database is Maktaba's own (above)
			out = map[string]any{"ok": true}
		case "freeze":
			// the machines' roles kept out first (a session may set itself read-write: a role
			// not let in has none), then every session read-only and the marker
			err = s.frozen(ctx, true)
			if err == nil {
				err = s.withSuper(func(db *sql.DB) error { return switchover.Freeze(ctx, db, req.ID) })
			}
		case "thaw":
			// the switch is over here (undone): the kit made for it goes, and another may start
			if err = s.withSuper(func(db *sql.DB) error { return switchover.Thaw(ctx, db) }); err == nil {
				err = s.frozen(ctx, false)
				_ = wipeFile(filepath.Join(s.dir(req.ID), "kit.chunks"))
				s.release(req.ID)
			}
		case "record":
			// the switch as this home's API runs it, kept where a restart finds it
			err = s.record(req.ID, req.Progress)
		case "recorded":
			out, err = s.recorded()
		case "new-home":
			// the promotion's answer did not come: the new home's notice to this machine
			out, err = s.newHomeOf(ctx, req)
		case "kit":
			out, err = s.kit(ctx, req)
		case "relay":
			// the cloud relay agent stops on the old home before the new one starts its
			// own (two agents of one server would take the tunnel from each other), and
			// starts again when the switch is undone
			out, err = s.relay(req.On)
		case "standby-kit":
			out, err = s.standbyKit(ctx, req)
		case "emergency-status":
			out = s.emergencyStatus(ctx)
		case "emergency":
			out, err = s.emergency(ctx, req)
		case "term-claim":
			out, err = s.termClaim(ctx, req)
		case "check-term":
			// a look for a newer home now, not at the watch's next minute (a switch called
			// off without the word of the machine that was to take over)
			out = map[string]any{"stepped_down": s.checkTerm(ctx, &http.Client{Timeout: 5 * time.Second})}
		case "kept-plan":
			out, err = s.keptPlan(ctx, req)
		case "kept-apply":
			out, err = s.keptApply(ctx, req)
		case "kept-dismiss":
			out, err = s.keptDismiss(ctx, req)
		case "demote":
			// answered first: the demotion ends with this machine starting again, the API
			// that asked among its processes; asked again while it runs, it says so, and
			// after one that failed it runs again (its error said)
			if err = s.claim(req.ID); err == nil {
				out = s.startDemote(req)
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

// release ends switchover id here: another may start.
func (s *switching) release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy == id {
		s.busy = ""
	}
}

// record keeps the switch as this home's API runs it (its progress), where the API finds
// it again after a restart: <state>/switchover/<id>/progress.json.
func (s *switching) record(id string, progress json.RawMessage) error {
	if id == "" || !json.Valid(progress) {
		return errors.New("a switch's progress")
	}
	return writePrivate(filepath.Join(s.dir(id), "progress.json"), progress)
}

// recorded are the switches this home's API recorded that did not end (handed_over,
// done or failed).
func (s *switching) recorded() (any, error) {
	paths, err := filepath.Glob(filepath.Join(s.cfg.StateDir(), "switchover", "*", "progress.json"))
	if err != nil {
		return nil, err
	}
	out := []json.RawMessage{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var st struct {
			State       string `json:"state"`
			ThawPending bool   `json:"thaw_pending"`
		}
		if json.Unmarshal(b, &st) != nil {
			continue
		}
		switch st.State {
		case "handed_over", "done":
			continue
		case "failed", "cancelled":
			if !st.ThawPending { // over (one whose thaw failed is asked again)
				continue
			}
		}
		out = append(out, b)
	}
	return map[string]any{"switches": out}, nil
}

// endRecord marks switchover id's record as ended here (state), so a restart of this
// machine's API does not take it up again.
func (s *switching) endRecord(id, state string) {
	path := filepath.Join(s.dir(id), "progress.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var p map[string]any
	if json.Unmarshal(b, &p) != nil {
		return
	}
	p["state"] = state
	if b, err = json.Marshal(p); err == nil {
		_ = writePrivate(path, b)
	}
}

// newHomeOf is the new home of switchover req.ID as the notice it sealed to this machine
// (its identity as one of its machines, chosen with the kit) says, read at the addresses
// the API knows for it: what the promotion's answer would have said, once it is the home
// (nil: not yet).
func (s *switching) newHomeOf(ctx context.Context, req homeRequest) (any, error) {
	me, err := readIdentity(filepath.Join(s.dir(req.ID), "old-home.json"))
	if err != nil {
		return nil, err
	}
	clusterPriv, err := os.ReadFile(clusterKeyPath())
	if err != nil || len(clusterPriv) != 32 {
		return nil, fmt.Errorf("this home's cluster key: %v", err)
	}
	clusterPub, err := curve25519.X25519(clusterPriv, curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	priv, err := base64.StdEncoding.DecodeString(me.PrivateKey)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, base := range req.URLs {
		var m struct {
			ID     string `json:"id"`
			Notice string `json:"notice"`
		}
		if base == "" || getJSON(ctx, client, strings.TrimRight(base, "/")+"/api/cluster/moved/"+url.PathEscape(me.NodeID), &m) != nil ||
			m.ID != req.ID {
			continue
		}
		plain, err := switchover.OpenNotice(priv, clusterPub, m.ID, m.Notice)
		if err != nil {
			continue
		}
		var notice movedNotice
		if json.Unmarshal(plain, &notice) == nil && notice.Term == req.Term && notice.Home.APIURL != "" {
			return map[string]any{"home": notice.Home}, nil
		}
	}
	return map[string]any{"home": nil}, nil
}

// frozen keeps the machines' database roles out of this home's Postgres (on) or lets
// them in again, at once (pgbundle.Bundle.SetFrozen, then a reload).
func (s *switching) frozen(ctx context.Context, on bool) error {
	if s.pg == nil {
		return nil
	}
	if err := s.pg.SetFrozen(on); err != nil {
		return fmt.Errorf("the machines' access: %w", err)
	}
	return s.withSuper(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `SELECT pg_reload_conf()`)
		return err
	})
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
	// On: the relay agent runs (relay)
	On bool `json:"on"`
	// a standby's kit: the hash of the one it holds, and whether to send it anyway
	Have  string `json:"have"`
	Force bool   `json:"force"`
	// an emergency: who asked, and the home's name they typed to confirm
	By      string `json:"by"`
	Confirm string `json:"confirm"`
	// what an old home kept aside: the report, and the plan the admin saw
	Report string `json:"report"`
	Plan   string `json:"plan"`
	// the API's record of a switch (record), and where the new home answers (new-home)
	Progress json.RawMessage `json:"progress"`
	URLs     []string        `json:"urls"`
	// another standby's claim on an emergency's term (term-claim): who, sealed by it
	From   string `json:"from"`
	Sealed string `json:"sealed"`
}

// relay starts or stops this home's cloud relay agent (none here: nothing to do).
func (s *switching) relay(on bool) (any, error) {
	if s.want == nil || !supervisor.RelayWanted(s.cfg) {
		return map[string]any{"relay": false}, nil
	}
	if err := s.want(supervisor.RoleRelay, on); err != nil {
		return nil, err
	}
	return map[string]any{"relay": true, "on": on}, nil
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
	// CA is the new home's database CA (PEM): the machines that follow it pin it;
	// TLSOff: its database has no TLS (its own [postgres] tls = "off").
	CA     string `json:"ca_pem,omitempty"`
	TLSOff bool   `json:"tls_off,omitempty"`
}

// database sets how n verifies this home's database: its CA, or no TLS.
func (h newHome) database(n *config.NodeConfig) {
	n.DatabaseCA, n.DatabaseTLS = h.CA, ""
	if h.TLSOff {
		n.DatabaseCA, n.DatabaseTLS = "", "off"
	}
}

// grant is a machine's copy following this new home from its slot (made by the
// promotion, before the new home's first write).
func (h newHome) grant(c nodecopy.Copy) nodecopy.Grant {
	g := nodecopy.Grant{Role: c.Role(), Slot: c.Slot(), SlotExists: true, Publication: c.Publication(),
		SystemID: h.SystemID, Database: pgbundle.AppDatabase}
	if h.CA != "" {
		ca := h.CA
		g.CA = &ca
	}
	return g
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
	files, err := s.homeFiles()
	if err != nil {
		return nil, err
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
	path := filepath.Join(dir, "kit.chunks")
	n, err := sealKit(k, clusterPriv, to, req.ID, path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "chunks": n, "old_home": me.NodeID}, nil
}

// homeFiles are the home's key files a new home needs (the ones this home has), the
// JWT keys its operator set, and its cloud relay's settings.
func (s *switching) homeFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	for name, path := range s.kitFiles() {
		if b, err := os.ReadFile(path); err == nil {
			files[name] = b
		}
	}
	// the home's JWT keys, when its operator set them (the new home's supervisor hands
	// them to its API from the keys directory)
	if priv, pub := os.Getenv("MAKTABA_JWT_PRIVATE_KEY_PEM"), os.Getenv("MAKTABA_JWT_PUBLIC_KEY_PEM"); priv != "" && pub != "" {
		files[kitJWTPrivate], files[kitJWTPublic] = []byte(priv), []byte(pub)
	}
	if len(files[kitSecretsKey]) == 0 {
		return nil, errors.New("this home has no secrets key to hand over")
	}
	// the cloud relay's settings (its claim was used already: never the code)
	if s.cfg.Cloud.Enabled {
		if b, err := json.Marshal(s.cfg.Cloud); err == nil {
			files[kitCloud] = b
		}
	}
	// a model server of another machine (one bundled here stays here: the new home runs
	// its own, when it has one)
	if u := strings.TrimSpace(s.cfg.LLM.URL); u != "" {
		if b, err := json.Marshal(kitLLMServer{URL: u, Models: s.cfg.LLM.Models}); err == nil {
			files[kitLLM] = b
		}
	}
	return files, nil
}

// sealKit seals k for the machine whose public key is to (the cluster key's private half
// clusterPriv) under id, into path: the number of chunks.
func sealKit(k *switchover.Kit, clusterPriv, to []byte, id, path string) (int, error) {
	key, err := switchover.KitKey(clusterPriv, to, id)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	n := 0
	sealer := switchover.NewSealer(key, id, func(i uint64, last bool, sealed []byte) error {
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
	return n, werr
}

// oldHome is this machine's identity as a machine of the new home (made once per
// switchover).
func (s *switching) oldHome(dir string) (oldHome, error) {
	return identityAt(filepath.Join(dir, "old-home.json"))
}

// identityAt is a machine identity kept at path, made there the first time.
func identityAt(path string) (oldHome, error) {
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

// handover follows the new home, again while its database does not answer yet (it
// starts again right after its promotion), until ctx ends.
func handover(ctx context.Context, c nodecopy.Copy, g nodecopy.Grant, local int64, logf func(string, ...any)) error {
	for tries := 1; ; tries++ {
		err := c.Handover(ctx, g, local)
		if err == nil {
			return nil
		}
		if tries == 1 {
			logf("the new home's database does not answer yet (%v): trying again", err)
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(2 * time.Second):
		}
	}
}

func writePrivate(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	// on disk before the rename, and the rename itself kept (the folder synced where the
	// system allows it): a switch's record must outlive a power cut that follows its answer
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir flushes a folder's entries (a rename in it) to disk. Windows cannot open a
// folder to sync it, and keeps a rename's entry on its own (NTFS's log): nothing to do.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// startDemote runs this home's demotion (demote) unless one runs: what it answers the
// API (the last one's error, when it failed).
func (s *switching) startDemote(req homeRequest) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.demoted == req.ID {
		return map[string]any{"accepted": true, "done": true}
	}
	if s.demoting {
		return map[string]any{"accepted": true, "running": true}
	}
	last := s.demoteErr
	s.demoting, s.demoteErr = true, ""
	go func() {
		err := s.demote(req)
		s.mu.Lock()
		s.demoting = false
		if err != nil {
			s.demoteErr = err.Error()
		}
		s.mu.Unlock()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[switchover] demote: %v (this home stays frozen; it is tried again)\n", err)
		}
	}()
	return map[string]any{"accepted": true, "last_error": last}
}

// demoteFollowWithin is how long the demotion waits for this database to receive the new
// home's changes before it gives up (nothing of this home's is undone then).
const demoteFollowWithin = 2 * time.Minute

// demote makes this home a machine of the new one: its database a copy of the new home's
// without copying again (it holds the marker); only once that copy receives the new
// home's changes does it give up what was the home's (its machines' slots and
// publications, its own tables, its keys); then its node.json is written and it starts
// again as a machine. Run again after a failure, it takes up where it was.
func (s *switching) demote(req homeRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, "[switchover] "+format+"\n", args...) }
	me, err := readIdentity(filepath.Join(s.dir(req.ID), "old-home.json"))
	if err != nil {
		return fmt.Errorf("this machine's identity: %w", err)
	}
	role := switchover.Machine{ID: me.NodeID}.Role()
	h := req.Home
	clusterPriv, err := os.ReadFile(clusterKeyPath())
	if err != nil {
		return fmt.Errorf("the cluster key: %w", err)
	}
	clusterPub, _ := curve25519.X25519(clusterPriv, curve25519.Basepoint)
	n := config.NodeConfig{NodeID: me.NodeID, Name: me.Name, PrivateKey: me.PrivateKey,
		DatabaseURL: (&url.URL{Scheme: "postgres", User: url.UserPassword(role, me.RolePassword),
			Host: net.JoinHostPort(h.DBHost, strconv.Itoa(h.DBPort)), Path: "/" + pgbundle.AppDatabase,
			RawQuery: "sslmode=prefer&connect_timeout=10"}).String(),
		Home: config.HomeRef{URL: h.APIURL, APIURL: h.APIURL, InstanceID: h.InstanceID, Name: h.Name,
			ClusterPub: base64.StdEncoding.EncodeToString(clusterPub), Term: req.Term}}
	// the new home's database, verified with the CA it handed over
	h.database(&n)
	dsn, err := n.HomeDSN(config.KeysDir())
	if err != nil {
		return fmt.Errorf("the new home's database: %w", err)
	}
	c := nodecopy.Copy{NodeID: me.NodeID, HomeDSN: dsn, DB: *s.pg}
	local, err := c.LocalSchema(ctx)
	if err != nil {
		return fmt.Errorf("this machine's schema: %w", err)
	}
	// this database serves its machines no more (following the new home, it would pass
	// the new home's changes on to them); its own data stays
	if err := s.withSuper(func(db *sql.DB) error { return switchover.StopServing(ctx, db) }); err != nil {
		return fmt.Errorf("the machines' slots: %w", err)
	}
	if err := handover(ctx, c, h.grant(c), local, logf); err != nil {
		return fmt.Errorf("following the new home: %w", err)
	}
	// nothing of the home's is given up before this database receives the new home's
	// changes: with the new home unreachable this home stays as it was, frozen
	end := time.Now().Add(demoteFollowWithin)
	for {
		ok, err := c.Receiving(ctx)
		if ok {
			break
		}
		if time.Now().After(end) {
			return fmt.Errorf("this database receives nothing from the new home (%v)", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err := s.withSuper(func(db *sql.DB) error { return switchover.Demote(ctx, db, me.NodeID) }); err != nil {
		return fmt.Errorf("giving up the home's own: %w", err)
	}
	// a machine's own copy: no machine reaches it (it starts again as one)
	if err := s.pg.SetFrozen(false); err != nil {
		logf("the machines' access: %v", err)
	}
	if err := writeNodeConfig(n); err != nil {
		return fmt.Errorf("node.json: %w", err)
	}
	// the home's keys are the new home's now: this machine keeps none of them (nor the
	// kit, nor its identity's file: node.json has it), and runs no relay agent
	s.wipeHomeKeys(req.ID, logf)
	if err := editConfig(func(c *config.Config) {
		if strings.EqualFold(strings.TrimSpace(c.Server.Role), "home") {
			c.Server.Role = ""
		}
		c.Cloud.Enabled = false
	}); err != nil {
		logf("server.toml: %v", err)
	}
	s.endRecord(req.ID, "handed_over")
	s.mu.Lock()
	s.demoted = req.ID
	s.mu.Unlock()
	logf("this machine is a machine of the new home now (%s): starting again", h.APIURL)
	go func() {
		time.Sleep(answerFirst) // the API hears it went through first
		requestReload()
	}()
	return nil
}

// homeKeyFiles are the key files that make a machine the home: once another machine is
// the home, this one keeps none of them.
func (s *switching) homeKeyFiles() []string {
	priv, pub := supervisor.JWTKeyPaths()
	caKey := filepath.Join(config.KeysDir(), kitCAKey)
	var serverTLS []string
	if s.pg != nil {
		caKey = filepath.Join(s.pg.Keys, kitCAKey)
		// a certificate the home's CA signed passes as the home's database to its machines
		serverTLS = s.pg.ServerTLSFiles()
	}
	// the Hugging Face token stays: it is the person's own, for this machine's models
	// (every machine keeps one of its own), not a key of the cluster's
	return append([]string{secretsKeyPath(), clusterKeyPath(), caKey, priv, pub, supervisor.RelayCredPath(),
		supervisor.RelayKeyPath()}, serverTLS...)
}

// wipeHomeKeys removes this machine's copies of the home's keys (overwritten first) and
// the kit and identity files of switchover id (with the ones an earlier release set
// aside as .was-home-*).
func (s *switching) wipeHomeKeys(id string, logf func(string, ...any)) {
	paths := s.homeKeyFiles()
	for _, p := range s.homeKeyFiles() {
		aside, _ := filepath.Glob(p + ".was-home-*")
		paths = append(paths, aside...)
	}
	if id != "" {
		paths = append(paths, filepath.Join(s.dir(id), "kit.chunks"), filepath.Join(s.dir(id), "old-home.json"))
	}
	for _, p := range paths {
		if err := wipeFile(p); err != nil {
			logf("removing %s: %v", p, err)
		}
	}
}

// wipeFile overwrites a file with zeros, flushes it and removes it (none: nothing to do).
func wipeFile(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		_, werr := f.Write(make([]byte, info.Size()))
		serr := f.Sync()
		_ = f.Close()
		if werr != nil || serr != nil {
			return errors.Join(werr, serr)
		}
	}
	return os.Remove(path)
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

// writeNodeConfig writes node.json with n's fields, keeping the others the file has
// (the pipeline's own, such as when the machine joined).
func writeNodeConfig(n config.NodeConfig) error {
	path := config.LocalNodeConfigPath()
	merged := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &merged)
	}
	fields, err := json.Marshal(n)
	if err != nil {
		return err
	}
	var ours map[string]json.RawMessage
	if err := json.Unmarshal(fields, &ours); err != nil {
		return err
	}
	for k, v := range ours {
		merged[k] = v
	}
	for _, k := range []string{"database_ca", "database_tls", "standby_homes", "peers"} {
		if _, set := ours[k]; !set {
			delete(merged, k) // omitted: none now
		}
	}
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(path, b)
}

// homeDatabase takes the home's database CA (or its word that the database has no
// TLS) over the machine channel, which the home's cluster key authenticates: node.json
// keeps it, and this machine starts again to reach its home verifying it (a machine
// joined before its home sealed the CA in the join, or the home's CA renewed). The home
// sends it to every machine now and then: the same one changes nothing.
func (s *switching) homeDatabase(args json.RawMessage) (ok bool, detail any) {
	var a struct {
		CA     string `json:"ca_pem"`
		TLSOff bool   `json:"tls_off"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return false, "bad arguments"
	}
	if !a.TLSOff {
		if err := config.CheckCA(a.CA); err != nil {
			return false, err.Error()
		}
	}
	n := supervisor.NodeConfig()
	if !n.Joined() {
		return false, "this machine has not joined a home"
	}
	ca, tls := n.DatabaseCA, n.DatabaseTLS
	newHome{CA: a.CA, TLSOff: a.TLSOff}.database(&n)
	if n.DatabaseCA == ca && n.DatabaseTLS == tls {
		return true, map[string]any{"changed": false}
	}
	if err := writeNodeConfig(n); err != nil {
		return false, err.Error()
	}
	fmt.Fprintln(os.Stderr, "[serve] node: the home's database certificate came over the machine channel: starting again with it")
	go func() {
		time.Sleep(time.Second) // the answer goes out first
		requestReload()
	}()
	return true, map[string]any{"changed": true}
}

// ---------- a machine's side: the home's commands over the channel ----------

// command answers the switchover's commands (machineagent.Command).
func (s *switching) command(ctx context.Context, name string, args json.RawMessage) (handled, ok bool, detail any) {
	switch name {
	case "home.database":
		ok, detail := s.homeDatabase(args)
		return true, ok, detail
	case "home.standbys":
		ok, detail := s.homeStandbys(args)
		return true, ok, detail
	}
	var a struct {
		ID    string  `json:"id"`
		Term  int64   `json:"term"`
		I     uint64  `json:"i"`
		Last  bool    `json:"last"`
		Data  string  `json:"data"`
		Home  newHome `json:"home"`
		Abort bool    `json:"abort"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.ID == "" {
		return strings.HasPrefix(name, "switch.") || strings.HasPrefix(name, "standby.") || name == "home.moved",
			false, "bad arguments"
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
	case "switch.status":
		out, err = s.status(ctx, a.ID, a.Term, a.Abort)
	case "home.moved":
		out, err = s.follow(ctx, a.ID, a.Term, a.Home)
	case "standby.kit":
		err = s.standbyChunk(a.ID, a.I, a.Last, a.Data)
	case "standby.commit":
		out, err = s.standbyCommit(a.ID, args)
	case "standby.forget":
		out, err = s.standbyForget(a.ID)
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
	return openSealed(filepath.Join(s.dir(id), "kit.chunks"), id, n)
}

// openSealed opens a kit sealed for this machine (n) under id, kept at path.
func openSealed(path, id string, n config.NodeConfig) (*switchover.Kit, error) {
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
	f, err := os.Open(path)
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

// calledOff and newHomeFile mark, in a switch's directory on the machine taking over,
// that its promotion will never run (the old home called it off), and what it answered
// once it went through.
func (s *switching) calledOff(id string) string   { return filepath.Join(s.dir(id), "called-off") }
func (s *switching) newHomeFile(id string) string { return filepath.Join(s.dir(id), "new-home.json") }

// sweepKits removes, at a start, the kits of switches over here: called off (this machine
// was to take over, and told the old home it would not), or promoted and answered. A kit
// whose switch this machine never heard more of is dropped when the old home asks again
// (it does, after an admin called it off: switch.status).
func (s *switching) sweepKits() {
	kits, _ := filepath.Glob(filepath.Join(s.cfg.StateDir(), "switchover", "*", "kit.chunks"))
	for _, k := range kits {
		id := filepath.Base(filepath.Dir(k))
		if fileExists(s.calledOff(id)) || fileExists(s.newHomeFile(id)) {
			_ = wipeFile(k)
		}
	}
}

// promote makes this machine the home: its copy promoted with the kit, the home's key
// files in place, its Postgres the bundled home's (on the LAN, TLS from the cluster's
// CA), node.json moved aside; answered with its addresses, then it starts again as the
// home. Asked again (its answer was lost), it answers the same. A promotion that fails
// leaves this copy as it was and calls the switch off here (the old home, asking,
// learns it), and this machine starts again as the old home's machine.
func (s *switching) promote(ctx context.Context, id string, term int64) (any, error) {
	if h, ok := s.promoted(id); ok {
		return h, nil
	}
	if err := s.claim(id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if _, err := os.Stat(s.calledOff(id)); err == nil {
		s.mu.Unlock()
		return nil, errors.New("this switch was called off: it does not run here")
	}
	s.promoting = id
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.promoting = ""
		s.mu.Unlock()
	}()
	fail := func(err error) (any, error) {
		_ = writePrivate(s.calledOff(id), nil)
		// the kit (the home's keys and secrets) stays on no machine but a standby home's
		_ = wipeFile(filepath.Join(s.dir(id), "kit.chunks"))
		s.release(id)
		go func() {
			time.Sleep(answerFirst) // the answer goes out first
			requestReload()
		}()
		return nil, err
	}
	n := supervisor.NodeConfig()
	kit, err := s.openKit(id, n)
	if err != nil {
		return fail(fmt.Errorf("the kit: %w", err))
	}
	if kit.Switchover != id || kit.Term != term {
		return fail(errors.New("the kit is another switchover's"))
	}
	if err := s.kitCA(kit); err != nil {
		return fail(err)
	}
	db, err := s.copyDB()
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	if s.stopCopy != nil {
		s.stopCopy()
	}
	done, err := switchover.Promote(ctx, db, switchover.Promotion{Self: n.NodeID, Name: n.Name, Term: term,
		Reason: "planned", Kit: kit, OldHome: kit.OldHome, SwitchID: id})
	if err != nil {
		return fail(err)
	}
	return s.becomeHome(ctx, db, kit, id, term, n, done)
}

// finishPromotion runs again the last steps of a promotion whose database went through
// (becomeHome).
func (s *switching) finishPromotion(ctx context.Context, id string, term int64) (newHome, error) {
	if fileExists(s.calledOff(id)) {
		// told the old home it called the switch off (it may take changes again): this
		// machine never becomes the home of it, its database promoted or not
		return newHome{}, errors.New("this switch was called off here: this machine does not finish its promotion")
	}
	if err := s.claim(id); err != nil {
		return newHome{}, err
	}
	s.mu.Lock()
	s.promoting = id
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.promoting = ""
		s.mu.Unlock()
	}()
	n := supervisor.NodeConfig()
	if !n.Joined() {
		// node.json moved aside already: the identity is in the one it was moved to
		path := config.LocalNodeConfigPath() + ".was-node-" + strconv.FormatInt(term-1, 10)
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &n)
		}
	}
	kit, err := s.openKit(id, n)
	if err != nil {
		return newHome{}, fmt.Errorf("the kit: %w", err)
	}
	db, err := s.copyDB()
	if err != nil {
		return newHome{}, err
	}
	defer db.Close()
	var done switchover.Promoted
	if err := db.QueryRowContext(ctx, `SELECT system_identifier::text FROM pg_control_system()`).Scan(&done.SystemID); err != nil {
		return newHome{}, err
	}
	return s.becomeHome(ctx, db, kit, id, term, n, done)
}

// promoted is what this machine answered switchover id's promotion, when it went
// through here.
func (s *switching) promoted(id string) (newHome, bool) {
	var h newHome
	b, err := os.ReadFile(s.newHomeFile(id))
	if err != nil || json.Unmarshal(b, &h) != nil || h.APIURL == "" {
		return newHome{}, false
	}
	return h, true
}

// status answers the old home, which did not get the promotion's answer: "home" (it went
// through: the addresses it answered), "promoting" (it runs), "called_off" (it did not and
// never will), "not_promoted" (not asked here, or not yet). With abort a switch not
// promoted here is called off first: its promotion never runs after.
func (s *switching) status(ctx context.Context, id string, term int64, abort bool) (any, error) {
	if h, ok := s.promoted(id); ok {
		return map[string]any{"state": "home", "home": h}, nil
	}
	// whether this machine's database was promoted (its copy's, or its own as a home now):
	// one that does not answer (starting, recovering) says nothing, and the switch is never
	// called off on it (the old home asks again); a machine with no database could not
	// have promoted
	var ok bool
	if s.cp != nil || s.pg != nil {
		var db *sql.DB
		var err error
		if s.cp != nil {
			db, err = s.copyDB()
		} else {
			db, err = superDB(*s.pg)
		}
		if err != nil {
			return nil, fmt.Errorf("whether this machine promoted is not known: %w", err)
		}
		ok, err = switchover.PromotedAt(ctx, db, supervisor.NodeConfig().NodeID, term)
		_ = db.Close()
		if err != nil {
			return nil, fmt.Errorf("whether this machine promoted is not known: %w", err)
		}
	}
	s.mu.Lock()
	running := s.promoting == id
	s.mu.Unlock()
	switch {
	case ok && running:
		return map[string]any{"state": "promoting"}, nil
	case ok:
		// the database is the home's, but the promotion's last steps did not finish
		// (its answer was not kept): they run again
		h, err := s.finishPromotion(ctx, id, term)
		if err != nil {
			return nil, err
		}
		return map[string]any{"state": "home", "home": h}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.promoting == id:
		return map[string]any{"state": "promoting"}, nil
	case fileExists(s.calledOff(id)):
		return map[string]any{"state": "called_off"}, nil
	case abort:
		if err := writePrivate(s.calledOff(id), nil); err != nil {
			return nil, err
		}
		_ = wipeFile(filepath.Join(s.dir(id), "kit.chunks"))
		if s.busy == id {
			s.busy = ""
		}
		return map[string]any{"state": "called_off"}, nil
	}
	return map[string]any{"state": "not_promoted"}, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// becomeHome finishes a promotion: the home's key files in place, this machine's
// Postgres the bundled home's (on the LAN, TLS from the cluster's CA), node.json moved
// aside, every machine's notice of the new home sealed; answered with its addresses, then
// it starts again as the home.
// kitCA checks the database CA the machines will verify this home by, before the
// promotion commits (a home they could not verify would have no machines): the kit's,
// or this machine's own when the kit carries none; none needed when the database has no
// TLS.
func (s *switching) kitCA(kit *switchover.Kit) error {
	if strings.EqualFold(strings.TrimSpace(s.cfg.Postgres.TLS), "off") {
		return nil
	}
	ca, ok := kit.Files[kitCACert]
	if !ok {
		b, err := os.ReadFile(s.cp.copy.DB.CACert())
		if err != nil {
			return fmt.Errorf("the database CA the machines would verify this home by: %w", err)
		}
		ca = b
	}
	if err := config.CheckCA(string(ca)); err != nil {
		return fmt.Errorf("the database CA the machines would verify this home by: %w", err)
	}
	return nil
}

func (s *switching) becomeHome(ctx context.Context, db *sql.DB, kit *switchover.Kit, id string, term int64,
	n config.NodeConfig, done switchover.Promoted) (newHome, error) {
	// the home's key files
	for name, path := range s.kitFiles() {
		if b, ok := kit.Files[name]; ok {
			if err := writePrivate(path, b); err != nil {
				return newHome{}, fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	// this machine's Postgres is the home's from its next start: the bundle where the copy
	// is, on the LAN with TLS, and no database url of its own; the cloud relay as the old
	// home had it (its supervisor runs the agent with the credentials that came along)
	db2 := s.cp.copy.DB
	var cloud *config.CloudSection
	if b, ok := kit.Files[kitCloud]; ok {
		var c config.CloudSection
		if json.Unmarshal(b, &c) == nil {
			cloud = &c
		}
	}
	var llm *kitLLMServer
	if b, ok := kit.Files[kitLLM]; ok {
		var l kitLLMServer
		if json.Unmarshal(b, &l) == nil && l.URL != "" {
			llm = &l
		}
	}
	if err := editConfig(func(c *config.Config) {
		c.Postgres.Enabled, c.Postgres.Home, c.Postgres.Port, c.Postgres.CopyPort = "on", db2.Home, db2.Port, 0
		c.Database.URL = ""
		if cloud != nil {
			// the old home's relay settings, but this machine's own status address
			claim, admin := c.Cloud.ClaimCode, c.Cloud.AdminAddr
			c.Cloud = *cloud
			c.Cloud.ClaimCode, c.Cloud.AdminAddr = claim, admin
		}
		if llm != nil && strings.TrimSpace(c.LLM.URL) == "" {
			// the old home's model server, unless this machine names its own
			c.LLM.URL = llm.URL
			if len(c.LLM.Models) == 0 {
				c.LLM.Models = llm.Models
			}
		}
	}); err != nil {
		return newHome{}, fmt.Errorf("server.toml: %w", err)
	}
	// the cluster's machines connect to it from its first start as the home, not one look
	// of its watch later (pgbundle.WatchReach)
	if err := db2.KeepReach(pgbundle.Reach{Open: true}); err != nil {
		fmt.Fprintf(os.Stderr, "[switchover] the machines reach this Postgres one look after its start: %v\n", err)
	}
	path := config.LocalNodeConfigPath()
	aside := path + ".was-node-" + strconv.FormatInt(term-1, 10)
	if err := os.Rename(path, aside); err != nil && (!os.IsNotExist(err) || !fileExists(aside)) {
		return newHome{}, fmt.Errorf("node.json: %w", err)
	}
	home := newHome{APIURL: s.cfg.PublicAPIURL(), WebURL: s.cfg.PublicWebURL(), DBPort: db2.Port,
		SystemID: done.SystemID, InstanceID: n.Home.InstanceID, Name: n.Name}
	if u, err := url.Parse(home.APIURL); err == nil {
		home.DBHost = u.Hostname()
	}
	if strings.EqualFold(strings.TrimSpace(s.cfg.Postgres.TLS), "off") {
		home.TLSOff = true
	} else {
		// the machines verify this database by its CA: without it they could not follow
		ca, err := os.ReadFile(db2.CACert())
		if err != nil {
			return newHome{}, fmt.Errorf("this database's CA: %w", err)
		}
		if err := config.CheckCA(string(ca)); err != nil {
			return newHome{}, fmt.Errorf("this database's CA %s: %w", db2.CACert(), err)
		}
		home.CA = string(ca)
	}
	// every machine's own notice of it, for one that is off now (the old home's too: the
	// cut is where what it wrote since begins; it also reads its own when the answer to
	// the promotion was lost)
	if err := sealNotices(ctx, db, kit, id, term, n.NodeID, home, done.CutLSN); err != nil {
		fmt.Fprintf(os.Stderr, "[switchover] the machines' notices of the new home: %v\n", err)
	}
	// its answer, the same when asked again; the kit is not needed any more
	if b, err := json.Marshal(home); err == nil {
		if err := writePrivate(s.newHomeFile(id), b); err != nil {
			return newHome{}, fmt.Errorf("keeping its answer: %w", err)
		}
	}
	_ = wipeFile(filepath.Join(s.dir(id), "kit.chunks"))
	fmt.Fprintf(os.Stderr, "[switchover] this machine is the home now (term %d): starting again\n", term)
	go func() {
		time.Sleep(answerFirst) // the answer goes out first
		requestReload()
	}()
	return home, nil
}

// movedNotice is what a switch's "moved" notice says: the term it began and the new home
// (the machine it was: HomeNode); to an old home an emergency took the home from, where in
// its WAL what it wrote since begins (Cut).
type movedNotice struct {
	Term     int64   `json:"term"`
	Home     newHome `json:"home"`
	HomeNode string  `json:"home_node,omitempty"`
	Cut      string  `json:"cut_lsn,omitempty"`
}

// sealNotices seals the new home's address for every machine of the cluster (by its
// public key) into the switch's row, which every copy has: a machine off during the
// switch reads its own from any machine it reaches, the old home among them, and
// follows (watchMoved).
func sealNotices(ctx context.Context, db *sql.DB, kit *switchover.Kit, id string, term int64, self string,
	home newHome, cut string) error {
	priv := kit.Files[kitClusterKey]
	if len(priv) != 32 {
		return errors.New("no cluster key in the kit")
	}
	plain, err := json.Marshal(movedNotice{Term: term, Home: home, HomeNode: self, Cut: cut})
	if err != nil {
		return err
	}
	out := map[string]string{}
	seal := func(nodeID, pubB64 string) error {
		if nodeID == self {
			return nil
		}
		pub, err := base64.StdEncoding.DecodeString(pubB64)
		if err != nil || len(pub) != 32 {
			return nil
		}
		out[nodeID], err = switchover.SealNotice(priv, pub, id, plain)
		return err
	}
	for _, raw := range kit.Tables["cluster_nodes"] {
		var row struct {
			ID  string `json:"id"`
			Pub string `json:"node_pubkey"`
		}
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		if err := seal(row.ID, row.Pub); err != nil {
			return err
		}
	}
	if m := kit.OldHome; m != nil {
		if err := seal(m.ID, m.PublicKey); err != nil {
			return err
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE cluster_switchovers SET notices = $2::jsonb, updated_at = now()
	    WHERE id = $1`, id, string(b))
	return err
}

// watchMoved finds the new home of this machine when it was off during a switch: its
// home answers as a machine now and hands it the notice the new home sealed for it; the
// machine follows (its copy anew: it missed the marker).
func (s *switching) watchMoved(ctx context.Context) {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.checkMoved(ctx, client)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *switching) checkMoved(ctx context.Context, client *http.Client) {
	n := supervisor.NodeConfig()
	home := strings.TrimRight(n.HomeURL(), "/")
	if home == "" || !n.Joined() {
		return
	}
	priv, err1 := base64.StdEncoding.DecodeString(n.PrivateKey)
	pub, err2 := base64.StdEncoding.DecodeString(n.Home.ClusterPub)
	if err1 != nil || err2 != nil {
		return
	}
	logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, "[switchover] "+format+"\n", args...) }
	// the home (a machine now, when it was switched), the standby homes, one of which may
	// have taken over while the home was gone (an emergency), and every other machine (its
	// copy has the notices: the old home may be gone for good)
	seen := map[string]bool{strings.TrimRight(s.cfg.PublicWebURL(), "/"): true}
	for _, base := range append(append([]string{home}, n.StandbyHomes...), n.Peers...) {
		base = strings.TrimRight(base, "/")
		if seen[base] {
			continue
		}
		seen[base] = true
		var m struct {
			ID     string `json:"id"`
			Notice string `json:"notice"`
		}
		if base == "" || getJSON(ctx, client, base+"/api/cluster/moved/"+url.PathEscape(n.NodeID), &m) != nil {
			continue
		}
		plain, err := switchover.OpenNotice(priv, pub, m.ID, m.Notice)
		if err != nil {
			logf("%s handed over a notice of a new home that does not open: %v", base, err)
			continue
		}
		var notice movedNotice
		if json.Unmarshal(plain, &notice) != nil || notice.Term <= n.Home.Term || notice.Home.APIURL == "" {
			continue
		}
		logf("the home moved to %s while this machine was away from it (term %d): following it", notice.Home.APIURL,
			notice.Term)
		if _, err := s.follow(ctx, m.ID, notice.Term, notice.Home); err != nil {
			logf("following the new home: %v", err)
		}
		return
	}
}

func getJSON(ctx context.Context, client *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// follow moves this machine to the new home: its copy without copying again when it
// holds the switch's marker (else it copies anew once it starts again), node.json
// pointed at the new home; then it starts again.
func (s *switching) follow(ctx context.Context, id string, term int64, h newHome) (any, error) {
	n := supervisor.NodeConfig()
	if n.Home.Term >= term && strings.TrimRight(n.Home.APIURL, "/") == strings.TrimRight(h.APIURL, "/") {
		// told again (the old home started again during the switch): it follows already
		return map[string]any{"copy_kept": true}, nil
	}
	u, err := url.Parse(n.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("node.json's database: %w", err)
	}
	u.Host = net.JoinHostPort(h.DBHost, strconv.Itoa(h.DBPort))
	n.DatabaseURL = u.String()
	// the new home's database, verified with the CA it handed over
	h.database(&n)
	dsn, err := n.HomeDSN(config.KeysDir())
	if err != nil {
		return nil, fmt.Errorf("the new home's database: %w", err)
	}
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
					hctx, cancel := context.WithTimeout(ctx, 90*time.Second)
					err = handover(hctx, c, h.grant(c), local, func(format string, args ...any) {
						fmt.Fprintf(os.Stderr, "[switchover] "+format+"\n", args...)
					})
					cancel()
				}
				if err != nil {
					return nil, fmt.Errorf("following the new home: %w", err)
				}
				moved = true
			}
		}
	}
	if !moved && s.cp != nil {
		// copied anew from the new home: the subscription to the old one paused (its slot
		// is gone)
		if err := s.cp.copy.Pause(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[switchover] pausing the copy of the old home: %v\n", err)
		}
	}
	n.Home.URL, n.Home.APIURL, n.Home.Name = h.APIURL, h.APIURL, h.Name
	if term > n.Home.Term {
		n.Home.Term = term
	}
	if err := writeNodeConfig(n); err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "[switchover] the home is %s now (copy kept: %v): starting again\n", h.APIURL, moved)
	go func() {
		time.Sleep(answerFirst)
		requestReload()
	}()
	return map[string]any{"copy_kept": moved}, nil
}
