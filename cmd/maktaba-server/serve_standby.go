package main

// Standby homes and the emergency (specs/epics/37-node-replica, "Emergency promotion"),
// as this machine's supervisor runs its part.
//
// On the home: its API keeps every standby home's kit current (gathered and sealed here,
// sent over the machine channel); every minute the home asks the standbys whether one of
// them took over while it was cut off or off (a notice sealed to it, of a newer term):
// then it stops writing, keeps what it wrote since the cut aside for the new home, and
// becomes one of its machines. On a new home after an emergency it plans and applies
// what the old home kept aside, when the admin asks.
//
// On a standby: the kit arrives over the channel and is kept sealed in the keys
// directory (a revoked machine's copy is wiped with it); its admin can make it the home
// when the home is gone, which promotes its copy with the kit, as a planned switch does,
// and tells every machine (and the old home) where the home is now.

import (
	"bytes"
	"context"
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
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/curve25519"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/nodecopy"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/switchover"
)

// ---------- the home's side ----------

// standbyIdentityPath is this home's identity as a machine of a standby that takes over
// in an emergency (made once, sealed in every standby's kit): it rejoins with it.
func (s *switching) standbyIdentityPath() string {
	return filepath.Join(s.cfg.StateDir(), "switchover", "standby-identity.json")
}

// standbyKit gathers the home's kit for a standby (the home running: the secrets, the
// machines and their roles, the key files, this home's identity as a machine) and,
// unless the standby holds it already (its stable hash: req.Have), seals it for that
// machine: <state>/switchover/standby-<node>.chunks, which the API sends over the channel.
func (s *switching) standbyKit(ctx context.Context, req homeRequest) (any, error) {
	to, err := base64.StdEncoding.DecodeString(req.ToPubKey)
	if err != nil || len(to) != 32 || req.ToNode == "" {
		return nil, errors.New("the machine's id and public key")
	}
	clusterPriv, err := os.ReadFile(clusterKeyPath())
	if err != nil || len(clusterPriv) != 32 {
		return nil, fmt.Errorf("this home's cluster key: %v", err)
	}
	me, err := identityAt(s.standbyIdentityPath())
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
	id := switchover.StandbyKitID(req.ToNode)
	var k *switchover.Kit
	err = s.withSuper(func(db *sql.DB) error {
		var term int64
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(max(term), 1) FROM cluster_terms`).Scan(&term); err != nil {
			return err
		}
		var err error
		k, err = switchover.GatherTables(ctx, db, id, term, files, switchover.StandbyTables)
		return err
	})
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	if me.Name == "" {
		me.Name = host
	}
	k.OldHome = &switchover.Machine{ID: me.NodeID, Name: me.Name, OS: runtime.GOOS, Arch: runtime.GOARCH,
		PublicKey: me.PublicKey, RolePassword: me.RolePassword, CopyPassword: copyPW, WebURL: s.cfg.PublicWebURL()}
	hash := k.StableHash()
	if !req.Force && req.Have == hash {
		return map[string]any{"hash": hash, "unchanged": true}, nil
	}
	path := filepath.Join(s.cfg.StateDir(), "switchover", "standby-"+filepath.Base(req.ToNode)+".chunks")
	n, err := sealKit(k, clusterPriv, to, id, path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"hash": hash, "path": path, "chunks": n, "term": k.Term, "made_at": k.MadeAt}, nil
}

// watchTerm is a home's watch for an emergency that took the home from it while it was
// cut off or off: every minute it asks each standby home it knows for a notice sealed to
// it (its identity in the standbys' kits); one of a newer term means another machine is
// the home now (stepDown).
func (s *switching) watchTerm(ctx context.Context) {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return
	}
	if s.pg == nil {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.checkTerm(ctx, client)
	}
}

func (s *switching) checkTerm(ctx context.Context, client *http.Client) {
	b, err := os.ReadFile(s.standbyIdentityPath())
	if err != nil {
		return // no standby ever had this home's kit
	}
	var me oldHome
	if json.Unmarshal(b, &me) != nil || me.NodeID == "" {
		return
	}
	var term int64
	var urls []string
	err = s.withSuper(func(db *sql.DB) error {
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(max(term), 1) FROM cluster_terms`).Scan(&term); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, `SELECT web_url FROM cluster_nodes WHERE standby AND state = 'approved'
		    AND COALESCE(web_url, '') <> ''`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u string
			if err := rows.Scan(&u); err == nil {
				urls = append(urls, u)
			}
		}
		return rows.Err()
	})
	if err != nil || len(urls) == 0 {
		return
	}
	clusterPriv, err := os.ReadFile(clusterKeyPath())
	if err != nil || len(clusterPriv) != 32 {
		return
	}
	clusterPub, err := curve25519.X25519(clusterPriv, curve25519.Basepoint)
	if err != nil {
		return
	}
	priv, err := base64.StdEncoding.DecodeString(me.PrivateKey)
	if err != nil {
		return
	}
	for _, base := range urls {
		var m struct {
			ID     string `json:"id"`
			Notice string `json:"notice"`
		}
		if getJSON(ctx, client, strings.TrimRight(base, "/")+"/api/cluster/moved/"+url.PathEscape(me.NodeID), &m) != nil {
			continue
		}
		plain, err := switchover.OpenNotice(priv, clusterPub, m.ID, m.Notice)
		if err != nil {
			continue
		}
		var notice movedNotice
		if json.Unmarshal(plain, &notice) != nil || notice.Term <= term || notice.Home.APIURL == "" {
			continue
		}
		if err := s.stepDown(ctx, me, m.ID, notice); err != nil {
			fmt.Fprintf(os.Stderr, "[switchover] giving the home up to %s: %v\n", notice.Home.APIURL, err)
		}
		return
	}
}

// keptReport is what an old home wrote after an emergency's cut, as it reports it to the
// new home (sealed: switchover.SealReport); kept in <state>/switchover until uploaded.
type keptReport struct {
	ID       string           `json:"id"`
	Term     int64            `json:"term"`
	FromNode string           `json:"from_node"`
	FromName string           `json:"from_name"`
	Cut      string           `json:"cut_lsn"`
	Lost     bool             `json:"lost"`
	Aside    switchover.Aside `json:"aside"`
	HomeAPI  string           `json:"home_api"`
	Uploaded bool             `json:"uploaded"`
}

// stepDown gives the home up to the machine that took over in an emergency: this home's
// database stops taking writes (Fence), what it wrote after the cut is read from the
// slot the new home's copy had here and kept (and reported, sealed, to the new home),
// and this machine becomes one of the new home's with the identity its kit gave it: its
// database is moved aside (never emptied) and it copies the new home's anew.
func (s *switching) stepDown(ctx context.Context, me oldHome, id string, notice movedNotice) error {
	if err := s.claim(id); err != nil {
		return err
	}
	logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, "[switchover] "+format+"\n", args...) }
	logf("another machine took over as the home (%s, term %d) while this one was away: keeping aside what it "+
		"wrote since, and becoming one of its machines", notice.Home.APIURL, notice.Term)
	report := keptReport{ID: uuid.NewString(), Term: notice.Term, FromNode: me.NodeID, FromName: me.Name,
		Cut: notice.Cut, HomeAPI: notice.Home.APIURL}
	err := s.withSuper(func(db *sql.DB) error {
		if err := switchover.Fence(ctx, db); err != nil {
			return err
		}
		slot := switchover.Machine{ID: notice.HomeNode}.Slot()
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)`,
			slot).Scan(&exists); err != nil {
			return err
		}
		if !exists || notice.HomeNode == "" {
			report.Lost = true
			return nil
		}
		aside, err := switchover.KeptAside(ctx, db, slot, slot, notice.Cut)
		if err != nil {
			return err
		}
		report.Aside = aside
		return nil
	})
	if err != nil {
		return err
	}
	if err := saveKept(s.cfg, report); err != nil {
		return fmt.Errorf("keeping what this home wrote since: %w", err)
	}
	if err := uploadKept(ctx, s.cfg, report.Term); err != nil {
		logf("the new home does not take this home's report yet (%v): it is sent again from here", err)
	}
	// this machine, one of the new home's
	clusterPriv, err := os.ReadFile(clusterKeyPath())
	if err != nil {
		return err
	}
	clusterPub, _ := curve25519.X25519(clusterPriv, curve25519.Basepoint)
	h := notice.Home
	role := switchover.Machine{ID: me.NodeID}.Role()
	n := config.NodeConfig{NodeID: me.NodeID, Name: me.Name, PrivateKey: me.PrivateKey,
		DatabaseURL: (&url.URL{Scheme: "postgres", User: url.UserPassword(role, me.RolePassword),
			Host: net.JoinHostPort(h.DBHost, strconv.Itoa(h.DBPort)), Path: "/" + pgbundle.AppDatabase,
			RawQuery: "sslmode=prefer&connect_timeout=10"}).String(),
		Home: config.HomeRef{URL: h.APIURL, APIURL: h.APIURL, InstanceID: h.InstanceID, Name: h.Name,
			ClusterPub: base64.StdEncoding.EncodeToString(clusterPub), Term: notice.Term}}
	h.database(&n)
	if err := writeNodeConfig(n); err != nil {
		return fmt.Errorf("node.json: %w", err)
	}
	// the home's keys are the new home's: this machine keeps none of them (what it wrote
	// since the cut is sealed to the cluster's public key, which node.json has)
	s.wipeHomeKeys("", logf)
	if err := editConfig(func(c *config.Config) {
		if strings.EqualFold(strings.TrimSpace(c.Server.Role), "home") {
			c.Server.Role = ""
		}
		c.Cloud.Enabled = false
	}); err != nil {
		logf("server.toml: %v", err)
	}
	logf("this machine is a machine of %s now: starting again (its database is set aside and the new home's copied)",
		h.APIURL)
	requestReload()
	return nil
}

func keptPath(cfg config.Config, term int64) string {
	return filepath.Join(cfg.StateDir(), "switchover", "kept-aside-"+strconv.FormatInt(term, 10)+".json")
}

func saveKept(cfg config.Config, r keptReport) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return writePrivate(keptPath(cfg, r.Term), b)
}

// uploadKept sends the report of term to the new home, sealed from this machine's key to
// the cluster's; once it took it, it is marked sent (the file stays: what this machine
// wrote).
func uploadKept(ctx context.Context, cfg config.Config, term int64) error {
	b, err := os.ReadFile(keptPath(cfg, term))
	if err != nil {
		return err
	}
	var r keptReport
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	if r.Uploaded {
		return nil
	}
	n := supervisor.NodeConfig()
	ident := n
	if ident.NodeID != r.FromNode {
		// not a machine yet: the identity this home's kits gave it
		me, err := readIdentity(filepath.Join(cfg.StateDir(), "switchover", "standby-identity.json"))
		if err != nil {
			return err
		}
		ident = config.NodeConfig{NodeID: me.NodeID, PrivateKey: me.PrivateKey}
	}
	priv, err := base64.StdEncoding.DecodeString(ident.PrivateKey)
	if err != nil {
		return err
	}
	clusterPub, err := clusterPublic(n)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(struct {
		keptReport
		Uploaded *bool `json:"uploaded,omitempty"`
	}{keptReport: r})
	if err != nil {
		return err
	}
	sealed, err := switchover.SealReport(priv, clusterPub, r.Term, plain)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"node_id": r.FromNode, "term": r.Term, "sealed": sealed})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.HomeAPI, "/")+"/api/cluster/kept-aside/report",
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("%s: %d %s", r.HomeAPI, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	r.Uploaded = true
	return saveKept(cfg, r)
}

// clusterPublic is the cluster's public key: node.json's, else this (old) home's own.
func clusterPublic(n config.NodeConfig) ([]byte, error) {
	if pub, err := base64.StdEncoding.DecodeString(n.Home.ClusterPub); err == nil && len(pub) == 32 {
		return pub, nil
	}
	for _, p := range []string{clusterKeyPath()} {
		if priv, err := os.ReadFile(p); err == nil && len(priv) == 32 {
			return curve25519.X25519(priv, curve25519.Basepoint)
		}
	}
	return nil, errors.New("the cluster's public key is not known here")
}

func readIdentity(path string) (oldHome, error) {
	var me oldHome
	b, err := os.ReadFile(path)
	if err != nil {
		return me, err
	}
	if err := json.Unmarshal(b, &me); err != nil || me.NodeID == "" {
		return me, fmt.Errorf("%s: not an identity", path)
	}
	return me, nil
}

// watchKept sends the reports of what this machine kept aside as a home that the new
// home did not take yet, every minute.
func (s *switching) watchKept(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		paths, _ := filepath.Glob(filepath.Join(s.cfg.StateDir(), "switchover", "kept-aside-*.json"))
		for _, p := range paths {
			term, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "kept-aside-"), ".json"), 10, 64)
			if err != nil {
				continue
			}
			_ = uploadKept(ctx, s.cfg, term)
		}
	}
}

// keptPlan is what applying a report of what an old home kept aside would do now.
func (s *switching) keptPlan(ctx context.Context, req homeRequest) (any, error) {
	var p switchover.Plan
	err := s.withSuper(func(db *sql.DB) error {
		var err error
		p, err = switchover.PlanReport(ctx, db, req.Report)
		return err
	})
	return p, err
}

// keptApply applies what the plan the admin saw applies (or, the plan changed since,
// nothing: the new plan with changed true); the fork is dropped once every report of
// its term is applied or set aside.
func (s *switching) keptApply(ctx context.Context, req homeRequest) (any, error) {
	var p switchover.Plan
	var changed bool
	err := s.withSuper(func(db *sql.DB) error {
		var err error
		p, err = switchover.ApplyReport(ctx, db, req.Report, req.Plan, userID(req.By))
		if errors.Is(err, switchover.ErrPlanChanged) {
			changed = true
			return nil
		}
		if err != nil {
			return err
		}
		s.dropForkIfDone(ctx, db, req.Report)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"plan": p, "changed": changed}, nil
}

// keptDismiss sets a report aside, nothing of it applied (it stays, to download).
func (s *switching) keptDismiss(ctx context.Context, req homeRequest) (any, error) {
	err := s.withSuper(func(db *sql.DB) error {
		if err := switchover.DismissReport(ctx, db, req.Report, userID(req.By)); err != nil {
			return err
		}
		s.dropForkIfDone(ctx, db, req.Report)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"dismissed": true}, nil
}

// dropForkIfDone drops the fork of report's term when no report of it waits any more.
func (s *switching) dropForkIfDone(ctx context.Context, db *sql.DB, report string) {
	var term int64
	var waiting int
	if db.QueryRowContext(ctx, `SELECT term FROM cluster_kept_aside WHERE id = $1`, report).Scan(&term) != nil {
		return
	}
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM cluster_kept_aside WHERE term = $1 AND state = 'reported'`, term).
		Scan(&waiting)
	if waiting == 0 {
		_ = switchover.DropFork(ctx, db, term)
	}
}

// ---------- a standby's side ----------

func standbyKitPath(name string) string { return filepath.Join(config.KeysDir(), name) }

// standbyChunk keeps one sealed chunk of the kit being received.
func (s *switching) standbyChunk(id string, i uint64, last bool, data string) error {
	n := supervisor.NodeConfig()
	if id != switchover.StandbyKitID(n.NodeID) {
		return errors.New("a kit for another machine")
	}
	sealed, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.KeysDir(), 0o700); err != nil {
		return err
	}
	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if i == 0 {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(standbyKitPath(nodecopy.StandbyKitPart), flag, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return switchover.WriteChunk(f, i, last, sealed)
}

// standbyMeta is what a standby's kit is (kept beside it).
type standbyMeta struct {
	Hash     string    `json:"hash"`
	Term     int64     `json:"term"`
	MadeAt   time.Time `json:"made_at"`
	Received time.Time `json:"received"`
	Home     string    `json:"home"`
}

// standbyCommit takes the kit received in full: it opens (it is the home's, for this
// machine), then replaces the one kept.
func (s *switching) standbyCommit(id string, args json.RawMessage) (any, error) {
	var a struct {
		Hash string `json:"hash"`
	}
	_ = json.Unmarshal(args, &a)
	n := supervisor.NodeConfig()
	if id != switchover.StandbyKitID(n.NodeID) {
		return nil, errors.New("a kit for another machine")
	}
	part := standbyKitPath(nodecopy.StandbyKitPart)
	k, err := openSealed(part, id, n)
	if err != nil {
		_ = os.Remove(part)
		return nil, fmt.Errorf("the kit does not open: %w", err)
	}
	if k.Switchover != id {
		_ = os.Remove(part)
		return nil, errors.New("the kit is another machine's")
	}
	if err := os.Rename(part, standbyKitPath(nodecopy.StandbyKitFile)); err != nil {
		return nil, err
	}
	meta := standbyMeta{Hash: a.Hash, Term: k.Term, MadeAt: k.MadeAt, Received: time.Now().UTC(), Home: n.Home.Name}
	b, _ := json.Marshal(meta)
	if err := writePrivate(standbyKitPath(nodecopy.StandbyKitMeta), b); err != nil {
		return nil, err
	}
	return map[string]any{"term": k.Term}, nil
}

// standbyForget removes the kit: this machine is no longer a standby home.
func (s *switching) standbyForget(id string) (any, error) {
	if id != switchover.StandbyKitID(supervisor.NodeConfig().NodeID) {
		return nil, errors.New("a kit for another machine")
	}
	for _, f := range []string{nodecopy.StandbyKitFile, nodecopy.StandbyKitMeta, nodecopy.StandbyKitPart} {
		if err := os.Remove(standbyKitPath(f)); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return map[string]any{"forgotten": true}, nil
}

// homeStandbys keeps where the standby homes are (node.json): with the home gone, this
// machine asks them for a notice of a new home.
func (s *switching) homeStandbys(args json.RawMessage) (bool, any) {
	var a struct {
		URLs  []string `json:"urls"`
		Peers []string `json:"peers"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return false, "bad arguments"
	}
	n := supervisor.NodeConfig()
	if !n.Joined() {
		return false, "this machine has not joined a home"
	}
	webURLs := func(in []string) []string {
		var keep []string
		for _, u := range in {
			if p, err := url.Parse(u); err == nil && (p.Scheme == "http" || p.Scheme == "https") && p.Host != "" {
				keep = append(keep, strings.TrimRight(u, "/"))
			}
		}
		return keep
	}
	keep, peers := webURLs(a.URLs), webURLs(a.Peers)
	if strings.Join(keep, " ") == strings.Join(n.StandbyHomes, " ") && strings.Join(peers, " ") == strings.Join(n.Peers, " ") {
		return true, map[string]any{"changed": false}
	}
	n.StandbyHomes, n.Peers = keep, peers
	if err := writeNodeConfig(n); err != nil {
		return false, err.Error()
	}
	return true, map[string]any{"changed": true}
}

// Reasons an emergency is not offered now.
const (
	emergencyNoKit       = "no_kit"
	emergencyHomeAnswers = "home_answers"
	emergencyNoCopy      = "no_copy"
	emergencyBusy        = "busy"
)

// emergencyState is whether this machine can take over as the home now.
type emergencyState struct {
	Standby     bool             `json:"standby"`
	KitTerm     int64            `json:"kit_term,omitempty"`
	KitMadeAt   *time.Time       `json:"kit_made_at,omitempty"`
	Home        string           `json:"home"`
	HomeURL     string           `json:"home_url"`
	HomeAnswers bool             `json:"home_answers"`
	Copy        *nodecopy.Status `json:"copy,omitempty"`
	Ready       bool             `json:"ready"`
	Reasons     []string         `json:"reasons,omitempty"`
}

func (s *switching) emergencyStatus(ctx context.Context) emergencyState {
	n := supervisor.NodeConfig()
	st := emergencyState{Home: n.Home.Name, HomeURL: n.HomeURL()}
	if b, err := os.ReadFile(standbyKitPath(nodecopy.StandbyKitMeta)); err == nil {
		var m standbyMeta
		if json.Unmarshal(b, &m) == nil {
			if _, err := os.Stat(standbyKitPath(nodecopy.StandbyKitFile)); err == nil {
				st.Standby, st.KitTerm = true, m.Term
				t := m.MadeAt
				st.KitMadeAt = &t
			}
		}
	}
	if !st.Standby {
		st.Reasons = append(st.Reasons, emergencyNoKit)
	}
	// the home must not answer: two homes never write
	var self struct {
		Role string `json:"role"`
	}
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if base := strings.TrimRight(st.HomeURL, "/"); base != "" &&
		getJSON(hctx, &http.Client{Timeout: 5 * time.Second}, base+"/api/cluster/self", &self) == nil && self.Role == "home" {
		st.HomeAnswers = true
		st.Reasons = append(st.Reasons, emergencyHomeAnswers)
	}
	if s.cp != nil {
		if _, ok := s.cp.copy.Mark(); ok {
			obs := s.cp.copy.Observe(ctx, time.Now())
			st.Copy = &obs
		}
	}
	if st.Copy == nil {
		st.Reasons = append(st.Reasons, emergencyNoCopy)
	}
	s.mu.Lock()
	if s.busy != "" {
		st.Reasons = append(st.Reasons, emergencyBusy)
	}
	s.mu.Unlock()
	st.Ready = len(st.Reasons) == 0
	return st
}

// emergency makes this standby the home, its home gone: answered at once, it runs in the
// background and ends with this machine starting again as the home.
func (s *switching) emergency(ctx context.Context, req homeRequest) (any, error) {
	st := s.emergencyStatus(ctx)
	if !st.Ready {
		return nil, fmt.Errorf("this machine cannot take over as the home now: %s", strings.Join(st.Reasons, ", "))
	}
	if !strings.EqualFold(strings.TrimSpace(req.Confirm), strings.TrimSpace(st.Home)) {
		return nil, errors.New("type the home's name to confirm")
	}
	id := uuid.NewString()
	if err := s.claim(id); err != nil {
		return nil, err
	}
	go func() {
		pctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := s.promoteEmergency(pctx, id, req.By); err != nil {
			fmt.Fprintf(os.Stderr, "[switchover] taking over as the home: %v\n", err)
			s.mu.Lock()
			s.busy = ""
			s.mu.Unlock()
		}
	}()
	return map[string]any{"accepted": true, "id": id}, nil
}

// promoteEmergency promotes this standby's copy with its kit: the sequences moved past
// what the old home may have written since the cut (EmergencyGap), the fork kept (what
// the old home kept aside applies only where this home changed nothing), the old home
// registered as a machine with the identity its kit gave it; the switch recorded with
// every machine's notice (the old home's with the cut).
func (s *switching) promoteEmergency(ctx context.Context, id, by string) error {
	n := supervisor.NodeConfig()
	kit, err := openSealed(standbyKitPath(nodecopy.StandbyKitFile), switchover.StandbyKitID(n.NodeID), n)
	if err != nil {
		return fmt.Errorf("the standby kit: %w", err)
	}
	db, err := s.copyDB()
	if err != nil {
		return err
	}
	defer db.Close()
	var term int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(max(term), 1) FROM cluster_terms`).Scan(&term); err != nil {
		return err
	}
	term = max(term, kit.Term) + 1
	oldName := n.Home.Name
	if s.stopCopy != nil {
		s.stopCopy()
	}
	// the switch, as the new home finishes it (switchover.Finish), written with the term
	done, err := switchover.Promote(ctx, db, switchover.Promotion{Self: n.NodeID, Name: n.Name, Term: term,
		Reason: "emergency", Kit: kit, OldHome: kit.OldHome, Gap: switchover.EmergencyGap, Fork: true,
		Record: &switchover.Record{ID: id, FromName: oldName, RequestedBy: userID(by)}})
	if err != nil {
		return err
	}
	if _, err := s.becomeHome(ctx, db, kit, id, term, n, done); err != nil {
		return err
	}
	// this machine holds the home's secrets itself now: no standby kit
	for _, f := range []string{nodecopy.StandbyKitFile, nodecopy.StandbyKitMeta, nodecopy.StandbyKitPart} {
		_ = os.Remove(standbyKitPath(f))
	}
	return nil
}

func userID(by string) string {
	if u, ok := strings.CutPrefix(by, "user:"); ok {
		if _, err := uuid.Parse(u); err == nil {
			return u
		}
	}
	return ""
}
