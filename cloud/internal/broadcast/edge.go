package broadcast

// The contract between the broadcast edge (web/packages/broadcast-edge) and the cloud API, one
// shape on both sides, locked by testdata/vectors.json (both the Go and the TS tests read it):
//
// Edge → cloud, every request signed with EDGE_SIGNING_KEY (the cloud keeps the public half,
// broadcast.edge_public_key, and checks each request with VerifyEdgeRequest; the edge's signer
// is web/packages/broadcast-edge/src/edge_sign.ts):
//
//	X-Maktaba-Edge-Time:      unix ms
//	X-Maktaba-Edge-Signature: base64url(Ed25519("maktaba-broadcast-edge-v1\n<METHOD>\n<path>\n<ms>\n<hex sha256 body>"))
//
//	POST /_edge/usage            UsagePost (at most MaxUsageWindows) → UsageReply
//	GET  /_edge/publishers/{id}  → billing.PublisherState
//
// Cloud → edge, cloud-signed control posts (tokens.go): PUT /v1/control/plan carries a Plan
// (SignPlan), PUT /v1/control/revocations the revocation list. The same Plan rides in every
// UsageReply, so the edge's plan state follows the cloud's at least every 5 minutes even
// before the cloud pushes it.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
)

const (
	EdgeDomain          = "maktaba-broadcast-edge-v1"
	HeaderEdgeTime      = "X-Maktaba-Edge-Time"
	HeaderEdgeSignature = "X-Maktaba-Edge-Signature"
	// EdgeMaxSkew is how far the edge's time may be from the cloud's.
	EdgeMaxSkew = 5 * time.Minute
	// UsagePath is the edge's usage post.
	UsagePath = "/_edge/usage"
	// MaxUsageWindows bounds one usage post: the edge sends a longer queue in pages.
	MaxUsageWindows = 2000
	// PlanTTL is how long the edge accepts a signed plan push.
	PlanTTL = 15 * time.Minute
)

// ErrEdgeSignature is a post the edge did not sign, or signed too long ago.
var ErrEdgeSignature = errors.New("broadcast: bad edge signature")

// EdgeSigningInput is what the edge signs for one request.
func EdgeSigningInput(method, path string, ms int64, body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%s\n%s\n%s\n%d\n%s", EdgeDomain, strings.ToUpper(method), path, ms, hex.EncodeToString(sum[:]))
}

// SignEdgeRequest sets the two headers the edge sends (the tests' and the rehearsal's fake edge;
// the Worker does the same with WebCrypto).
func SignEdgeRequest(priv ed25519.PrivateKey, h http.Header, method, path string, ms int64, body []byte) {
	h.Set(HeaderEdgeTime, strconv.FormatInt(ms, 10))
	h.Set(HeaderEdgeSignature, b64.EncodeToString(ed25519.Sign(priv, []byte(EdgeSigningInput(method, path, ms, body)))))
}

// VerifyEdgeRequest checks a post's edge signature and time.
func VerifyEdgeRequest(pub ed25519.PublicKey, method, path string, h http.Header, body []byte, now time.Time) error {
	ms, err := strconv.ParseInt(h.Get(HeaderEdgeTime), 10, 64)
	if err != nil {
		return fmt.Errorf("%w: no time", ErrEdgeSignature)
	}
	if d := now.Sub(time.UnixMilli(ms)); d > EdgeMaxSkew || d < -EdgeMaxSkew {
		return fmt.Errorf("%w: time off by %s", ErrEdgeSignature, d)
	}
	sig, err := b64.DecodeString(h.Get(HeaderEdgeSignature))
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, []byte(EdgeSigningInput(method, path, ms, body)), sig) {
		return ErrEdgeSignature
	}
	return nil
}

// UsageWindow is one publisher's 5-minute window as the edge posts it (publisher.ts's
// UsageWindow, the same fields in the same order). Key is the idempotency key
// <publisher>:<window_start> (billing.WindowKey: RFC 3339 UTC, whole seconds); a window posted
// again under the same key is counted once. The cloud bills minutes_full and minutes_lowest;
// the others are the edge's counters, checked and not stored yet.
type UsageWindow struct {
	Key           string    `json:"key"`
	Publisher     string    `json:"publisher"`
	WindowStart   time.Time `json:"window_start"`
	WindowEnd     time.Time `json:"window_end"`
	MinutesFull   int64     `json:"minutes_full"`
	MinutesLowest int64     `json:"minutes_lowest"`
	// MinutesStream is the viewer-minutes that went through the Stream fallback.
	MinutesStream int64 `json:"minutes_stream"`
	SegmentsIn    int64 `json:"segments_in"`
	BytesIn       int64 `json:"bytes_in"`
	PeakSessions  int64 `json:"peak_sessions"`
}

// ErrWindowShape is a window whose key, end or counters do not match its start.
var ErrWindowShape = errors.New("broadcast: a window's key must be <publisher>:<window_start>, its end 5 minutes after its start, its counters not negative")

// Billing is the window as the cloud bills it, once its shape is checked.
func (w UsageWindow) Billing() (billing.UsageWindow, error) {
	if w.Key != billing.WindowKey(w.Publisher, w.WindowStart) || !w.WindowEnd.Equal(w.WindowStart.Add(billing.WindowLength)) ||
		w.MinutesStream < 0 || w.SegmentsIn < 0 || w.BytesIn < 0 || w.PeakSessions < 0 {
		return billing.UsageWindow{}, ErrWindowShape
	}
	return billing.UsageWindow{Publisher: w.Publisher, Window: w.WindowStart, MinutesFull: w.MinutesFull, MinutesLowest: w.MinutesLowest}, nil
}

// UsagePost is the edge's usage post.
type UsagePost struct {
	Edge    string        `json:"edge"`
	Windows []UsageWindow `json:"windows"`
}

// Rejected is a window that will never be taken: the edge drops it.
type Rejected struct {
	Key   string `json:"key"`
	Error string `json:"error"`
}

// UsageReply is the cloud's answer to a usage post. Every key is a posted window's own key: the
// edge marks taken, duplicate and rejected windows done and keeps any other for the next post.
// Plans is each publisher's plan state after its windows, the same Plan a push carries.
type UsageReply struct {
	Taken      []string                 `json:"taken"`
	Duplicates []string                 `json:"duplicates"`
	Rejected   []Rejected               `json:"rejected"`
	States     []billing.PublisherState `json:"states"`
	Plans      []Plan                   `json:"plans"`
}

// Plan is what the edge's PublisherControl holds for a publisher (publisher.ts's PlanState):
// the mode a NEW viewer session gets and the limits the edge enforces. Version orders them: the
// edge never replaces a newer plan with an older one.
type Plan struct {
	Pub        string `json:"pub"`
	Version    int64  `json:"version"`
	Mode       string `json:"mode"`
	MaxRungs   int    `json:"max_rungs,omitempty"`
	MaxTopKbps int    `json:"max_top_kbps,omitempty"`
	MaxViewers int    `json:"max_viewers,omitempty"`
	// Delivery is a per-publisher delivery override (stream or r2); empty follows the edge's
	// BROADCAST_DELIVERY.
	Delivery string `json:"delivery,omitempty"`
}

// PlanFromState is the plan the edge holds for a publisher's state; its version is the state's
// time in unix ms, so a later state always wins.
func PlanFromState(st billing.PublisherState) Plan {
	return Plan{Pub: st.Publisher, Version: st.At.UnixMilli(), Mode: st.Mode, MaxRungs: st.MaxRungs,
		MaxTopKbps: st.MaxTopKbps, MaxViewers: st.MaxConcurrentViewers}
}

// SignPlan signs a plan push for the edge's PUT /v1/control/plan.
func SignPlan(k Key, p Plan, now time.Time) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // a ms version stays an integer
	if err := dec.Decode(&m); err != nil {
		return "", err
	}
	return SignControl(k, "plan", m, now, PlanTTL)
}
