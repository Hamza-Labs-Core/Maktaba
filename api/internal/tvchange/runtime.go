package tvchange

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// The staging runtime (story 33.5): the home's front door runs a staged change's
// release on the third TV slot instance, warms it and samples its output; the
// controller reads those samples and writes the change's verify report. This is
// the controller's side of POST /internal/tv/staging/start|stop and GET
// /internal/tv/staging (signed with the cluster stream key, loopback).

// ErrNoStagingRuntime: the front door runs no TV slots (one process, [tv] slots
// off) or staging is not built / switched off there: verification stays on the
// database (the interim of stories 33.2-33.12).
var ErrNoStagingRuntime = errors.New("tvchange: no staging slot runtime")

// StagingRuntime is the front door's staging.
type StagingRuntime interface {
	Start(ctx context.Context, plan StagingPlan) (*StagingView, error)
	Stop(ctx context.Context, changeID string) error
	View(ctx context.Context) (*StagingView, error)
}

// StagingPlan is what is staged (streaming frontdoor.StagingPlan).
type StagingPlan struct {
	ChangeID     string   `json:"change_id"`
	ReleaseID    string   `json:"release_id"`
	Version      string   `json:"version"`
	SHA256       string   `json:"sha256"`
	Channels     []string `json:"channels"`
	Changed      []string `json:"changed"`
	Removed      []string `json:"removed"`
	Probe        string   `json:"probe"`
	ProbeSeconds int      `json:"probe_seconds"`
	MaxExtra     int      `json:"max_extra"`
}

// SlotStatus is the part of a slot's status the checks read.
type SlotStatus struct {
	Slot         string        `json:"slot"`
	Role         string        `json:"role"`
	Release      string        `json:"release"`
	Mismatch     bool          `json:"release_mismatch"`
	Version      string        `json:"version"`
	PID          int           `json:"pid"`
	StartedAt    string        `json:"started_at"`
	Stations     []string      `json:"stations"`
	Channels     []StationInfo `json:"channels"`
	EncodersUsed int           `json:"encoders_used"`
}

// StationInfo is one station of a slot.
type StationInfo struct {
	ID             string     `json:"id"`
	Encoder        string     `json:"encoder"`
	Restarts       int        `json:"restarts"`
	NVENCFallbacks int64      `json:"nvenc_fallbacks"`
	Reached        *time.Time `json:"reached"`
	Block          *OnAir     `json:"block"`
}

// OnAir is the block a station airs.
type OnAir struct {
	Seq       int64     `json:"seq"`
	Kind      string    `json:"kind"`
	VideoID   string    `json:"video_id"`
	StartAt   time.Time `json:"start_at"`
	EndAt     time.Time `json:"end_at"`
	Title     string    `json:"title"`
	Synthetic bool      `json:"synthetic"`
	// StandInFor is the seq of the schedule block this one airs in place of, its
	// file unreadable on the slot's host (a filler or a slate); 0: none.
	StandInFor int64 `json:"stand_in_for"`
}

// ProbeResult is one front-door check of a channel.
type ProbeResult struct {
	Check  string `json:"check"`
	Result string `json:"result"`
	Value  string `json:"value"`
}

// ChannelSample is the front door's latest measurement of a staging channel.
type ChannelSample struct {
	Channel           string          `json:"channel"`
	Why               string          `json:"why"`
	WarmSince         time.Time       `json:"warm_since"`
	At                time.Time       `json:"at"`
	Station           *StationInfo    `json:"station"`
	StationAt         time.Time       `json:"station_at"`
	LiveStation       *StationInfo    `json:"live_station"`
	Results           []ProbeResult   `json:"results"`
	Cloudflare        []string        `json:"cloudflare"`
	Params            []string        `json:"params"`
	FirstSegmentAfter float64         `json:"first_segment_after"`
	Staging           json.RawMessage `json:"staging,omitempty"`
	Live              json.RawMessage `json:"live,omitempty"`
}

// WarmEntry is one channel of the staging warm set.
type WarmEntry struct {
	Channel string    `json:"channel"`
	Why     string    `json:"why"`
	Since   time.Time `json:"since"`
}

// StagingView is GET /internal/tv/staging.
type StagingView struct {
	Active      bool                      `json:"active"`
	Plan        *StagingPlan              `json:"plan"`
	Slot        string                    `json:"slot"`
	StartedAt   time.Time                 `json:"started_at"`
	Healthy     bool                      `json:"healthy"`
	Status      *SlotStatus               `json:"status"`
	Budget      int                       `json:"budget"`
	Capacity    int                       `json:"capacity"`
	LiveOnAir   int                       `json:"live_on_air"`
	StandbyWarm int                       `json:"standby_warm"`
	Warm        []WarmEntry               `json:"warm"`
	Pending     []string                  `json:"pending"`
	Probed      []string                  `json:"probed"`
	Deferred    []string                  `json:"deferred"`
	Samples     map[string]*ChannelSample `json:"samples"`
	Now         time.Time                 `json:"now"`
}

// HTTPStaging talks to the home's front door.
type HTTPStaging struct {
	DB     *sql.DB
	Base   string
	Client *http.Client
	Now    func() time.Time
}

// NewHTTPStaging reads MAKTABA_STREAMING_HTTP_URL (default the home's 8081).
func NewHTTPStaging(db *sql.DB) *HTTPStaging {
	u := strings.TrimRight(os.Getenv("MAKTABA_STREAMING_HTTP_URL"), "/")
	if u == "" {
		u = "http://127.0.0.1:8081"
	}
	return &HTTPStaging{DB: db, Base: u}
}

// streamKey is the cluster stream key (tv_cluster_secret).
func streamKey(ctx context.Context, db *sql.DB) ([]byte, error) {
	var key []byte
	if err := db.QueryRowContext(ctx, `SELECT stream_key FROM tv_cluster_secret WHERE id = 1`).Scan(&key); err != nil {
		return nil, errors.New("no cluster stream key yet (the home's streaming service creates it at boot)")
	}
	return key, nil
}

// frontSign is the front-door signature (streaming cluster.Keyring.Sign).
func frontSign(key []byte, method, path string, exp time.Time) string {
	e := strconv.FormatInt(exp.Unix(), 10)
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(method + "\n" + path + "\n" + e + "\n0"))
	return e + "." + hex.EncodeToString(m.Sum(nil))
}

// PreviewToken mints a staging preview token (streaming cluster.PreviewToken):
// the change id, its expiry and the HMAC with the cluster stream key.
func PreviewToken(ctx context.Context, db *sql.DB, changeID string, exp time.Time) (string, error) {
	key, err := streamKey(ctx, db)
	if err != nil {
		return "", err
	}
	e := strconv.FormatInt(exp.Unix(), 10)
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte("preview\n" + changeID + "\n" + e))
	return changeID + "." + e + "." + hex.EncodeToString(m.Sum(nil)), nil
}

func (h *HTTPStaging) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *HTTPStaging) do(ctx context.Context, method, path string, body any, out any) error {
	key, err := streamKey(ctx, h.DB)
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, h.Base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Maktaba-Front", frontSign(key, method, path, h.now().Add(60*time.Second)))
	cl := h.Client
	if cl == nil {
		cl = http.DefaultClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("the front door did not answer: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	msg := strings.TrimSpace(string(b))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNoStagingRuntime, msg)
	case resp.StatusCode == http.StatusConflict && strings.Contains(msg, "staging is off"):
		return fmt.Errorf("%w: %s", ErrNoStagingRuntime, msg)
	case resp.StatusCode >= 300:
		return fmt.Errorf("the front door answered %d: %s", resp.StatusCode, msg)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Start implements StagingRuntime.
func (h *HTTPStaging) Start(ctx context.Context, plan StagingPlan) (*StagingView, error) {
	var v StagingView
	if err := h.do(ctx, http.MethodPost, "/internal/tv/staging/start", plan, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Stop implements StagingRuntime.
func (h *HTTPStaging) Stop(ctx context.Context, changeID string) error {
	return h.do(ctx, http.MethodPost, "/internal/tv/staging/stop", map[string]string{"change_id": changeID}, nil)
}

// View implements StagingRuntime.
func (h *HTTPStaging) View(ctx context.Context) (*StagingView, error) {
	var v StagingView
	if err := h.do(ctx, http.MethodGet, "/internal/tv/staging", nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}
