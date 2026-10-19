package cluster

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// ProtocolVersion is the streaming cluster protocol (signing, headers, routes). A host
// whose protocol differs from the home's is unfit (unfit: version).
const ProtocolVersion = 2 // 2: the placement generation is part of the signature (Epic 33)

// BeatInterval is how often each streaming host calls tv_host_beat.
const BeatInterval = 5 * time.Second

// Unfit reasons a host reports.
const (
	UnfitStorage = "storage"
	UnfitVersion = "version"
)

// BeatInfo is what a host reports every BeatInterval (design §2.2).
type BeatInfo struct {
	Host          string          `json:"host"`
	Role          string          `json:"role"`
	Version       string          `json:"version"`
	Encoder       string          `json:"encoder,omitempty"`
	Encoders      []string        `json:"encoders"`
	Capacity      int             `json:"capacity"`
	Active        []string        `json:"active"`
	Load          map[string]any  `json:"load"`
	StreamBaseURL string          `json:"stream_base_url,omitempty"`
	CanPush       map[string]bool `json:"can_push"`
	CanLive       bool            `json:"can_live"`
	Unfit         string          `json:"unfit,omitempty"`
	NVENCFree     int             `json:"nvenc_free"`
	VTFree        int             `json:"vt_free"`
}

// BeatAnswer is what tv_host_beat returns: the admin's switches and the stream key.
type BeatAnswer struct {
	Enabled          bool   `json:"enabled"`
	Draining         bool   `json:"draining"`
	CapacityOverride *int   `json:"capacity_override"`
	Weight           int    `json:"weight"`
	StreamBaseURL    string `json:"stream_base_url"`
	KeyB64           string `json:"stream_key_b64"`
	PrevKeyB64       string `json:"previous_key_b64"`
}

// Key decodes the stream key (nil when the home has not created it yet).
func (a BeatAnswer) Key() []byte {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(a.KeyB64))
	if err != nil {
		return nil
	}
	return b
}

// BeatStore records a beat (Postgres: tv_host_beat).
type BeatStore interface {
	Beat(ctx context.Context, hostID string, info BeatInfo) (BeatAnswer, error)
}

// PGBeatStore calls tv_host_beat (slot 0123).
type PGBeatStore struct{ Pool *pgxpool.Pool }

func (s PGBeatStore) Beat(ctx context.Context, hostID string, info BeatInfo) (BeatAnswer, error) {
	if info.Active == nil {
		info.Active = []string{}
	}
	if info.Encoders == nil {
		info.Encoders = []string{}
	}
	b, err := json.Marshal(info)
	if err != nil {
		return BeatAnswer{}, err
	}
	var raw []byte
	if err := s.Pool.QueryRow(ctx, `SELECT tv_host_beat($1, $2::jsonb)::text`, hostID, string(b)).Scan(&raw); err != nil {
		return BeatAnswer{}, err
	}
	var a BeatAnswer
	err = json.Unmarshal(raw, &a)
	return a, err
}

// EnsureSecret creates the cluster stream key (32 random bytes) when there is none.
// Home only (the machines' role cannot write tv_cluster_secret).
func EnsureSecret(ctx context.Context, pool *pgxpool.Pool) error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, `INSERT INTO tv_cluster_secret (id, stream_key) VALUES (1, $1) ON CONFLICT (id) DO NOTHING`, key)
	return err
}

// HostLoop beats every Interval, keeps the last answer and installs the stream key.
type HostLoop struct {
	Store    BeatStore
	HostID   string
	Interval time.Duration
	Info     func(ctx context.Context) BeatInfo
	Keyring  *Keyring
	Logger   *slog.Logger
	// OnAnswer, when set, is called after each successful beat.
	OnAnswer func(BeatAnswer)

	mu     sync.Mutex
	last   BeatAnswer
	have   bool
	failed bool
}

// Answer is the last beat's answer and whether there was one.
func (l *HostLoop) Answer() (BeatAnswer, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last, l.have
}

// BeatOnce sends one beat.
func (l *HostLoop) BeatOnce(ctx context.Context) error {
	if l.Store == nil || l.Info == nil {
		return errors.New("cluster: host loop not configured")
	}
	bctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	a, err := l.Store.Beat(bctx, l.HostID, l.Info(bctx))
	l.mu.Lock()
	wasFailed := l.failed
	l.failed = err != nil
	if err == nil {
		l.last, l.have = a, true
	}
	l.mu.Unlock()
	log := l.Logger
	if log == nil {
		log = slog.Default()
	}
	if err != nil {
		if !wasFailed {
			log.Warn("tv: heartbeat failed", "host_id", l.HostID, "err", err.Error(), "event", "tv_host_beat_failed")
		}
		return err
	}
	if wasFailed {
		log.Info("tv: heartbeat back", "host_id", l.HostID, "event", "tv_host_beat_ok")
	}
	if l.Keyring != nil {
		l.Keyring.Set(a.Key())
	}
	if l.OnAnswer != nil {
		l.OnAnswer(a)
	}
	return nil
}

// Run beats until ctx ends.
func (l *HostLoop) Run(ctx context.Context) {
	iv := l.Interval
	if iv <= 0 {
		iv = BeatInterval
	}
	_ = l.BeatOnce(ctx)
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = l.BeatOnce(ctx)
		}
	}
}

// NodeSettings is how a streaming node runs from its Epic 31 node config.
type NodeSettings struct {
	NodeID      string
	Name        string
	DatabaseURL string
	ConfigPath  string
}

type nodeFile struct {
	NodeID      string `json:"node_id"`
	Name        string `json:"name"`
	DatabaseURL string `json:"database_url"`
}

// NodeMode reports whether this streaming process runs as a cluster node and with
// what (AC2): MAKTABA_TV_HOST_ROLE=node, or a joined node config (channel.DetectRole).
// The database URL and node id come from the node config; DATABASE_URL and
// MAKTABA_NODE_ID override them (two nodes on one machine, tests).
func NodeMode(getenv func(string) string, goos, userHome string, readFile func(string) ([]byte, error)) (NodeSettings, bool) {
	path := channel.NodeConfigPath(getenv, goos, userHome)
	var nf nodeFile
	if b, err := readFile(path); err == nil {
		_ = json.Unmarshal(b, &nf)
	}
	role := strings.ToLower(strings.TrimSpace(getenv("MAKTABA_TV_HOST_ROLE")))
	if role == "" {
		role, _ = channel.DetectRole(getenv, goos, userHome, readFile)
	}
	if role != channel.RoleNode {
		return NodeSettings{}, false
	}
	ns := NodeSettings{NodeID: strings.TrimSpace(nf.NodeID), Name: strings.TrimSpace(nf.Name),
		DatabaseURL: strings.TrimSpace(nf.DatabaseURL), ConfigPath: path}
	if v := strings.TrimSpace(getenv("MAKTABA_NODE_ID")); v != "" {
		ns.NodeID = v
	}
	if v := strings.TrimSpace(getenv("DATABASE_URL")); v != "" {
		ns.DatabaseURL = v
	}
	return ns, true
}

// NodeModeFromOS is NodeMode for this process.
func NodeModeFromOS(goos string) (NodeSettings, bool) {
	home, _ := os.UserHomeDir()
	return NodeMode(os.Getenv, goos, home, os.ReadFile)
}

// NodePathMap reads the node's path map from cluster_node_beat (inferred or set by the
// admin, Epic 31): a JSON object {db_prefix: local_prefix}.
func NodePathMap(ctx context.Context, pool *pgxpool.Pool, nodeID string) (pathmap.Map, error) {
	var raw []byte
	err := pool.QueryRow(ctx, `SELECT COALESCE(path_map, '{}'::jsonb)::text FROM cluster_node_beat($1::uuid)`, nodeID).Scan(&raw)
	if err != nil {
		return pathmap.Map{}, err
	}
	return pathmap.FromJSON(raw)
}

// StorageFit samples up to five videos and checks that at least one opens here
// through the path map (the read half of Epic 31's storage check). "" when it reads
// (or there is nothing to read), UnfitStorage otherwise.
func StorageFit(ctx context.Context, pool *pgxpool.Pool, pm pathmap.Map, stat func(string) (os.FileInfo, error)) string {
	if stat == nil {
		stat = os.Stat
	}
	rows, err := pool.Query(ctx, `SELECT path FROM videos WHERE path IS NOT NULL AND path <> '' LIMIT 5`)
	if err != nil {
		return "" // cannot sample: do not hold the node out for it
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var p string
		if rows.Scan(&p) != nil {
			continue
		}
		n++
		if fi, err := stat(pm.Apply(p)); err == nil && !fi.IsDir() {
			return ""
		}
	}
	if n == 0 {
		return ""
	}
	return UnfitStorage
}
