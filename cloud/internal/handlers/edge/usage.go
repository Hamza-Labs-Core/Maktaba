// Package edge (handlers) is what the broadcast edge (story 50.21's multi-tenant Worker) calls
// on the cloud API (story 50.20 §3). Every request is signed with the edge's Ed25519 key; the
// cloud holds only the public half (broadcast.edge_public_key). Without it, every route is 503.
//
// Signing: the edge sends
//
//	X-Maktaba-Edge-Timestamp: <unix seconds>
//	X-Maktaba-Edge-Signature: base64(ed25519(<timestamp> "\n" <METHOD> " " <path> "\n" <body>))
//
// and the cloud refuses a timestamp more than 5 minutes from its clock (401 bad_edge_signature).
//
//	POST /_edge/usage                 {"edge": "<id>", "windows": [UsageWindow, ...]} (at most 2,000)
//	                                  → 200 {"taken": [key], "duplicates": [key],
//	                                         "rejected": [{"key", "error"}], "states": [PublisherState]}
//	GET  /_edge/publishers/{id}       → 200 PublisherState
//
// A window's key is <publisher>:<window> (RFC 3339 UTC start of its 5 minutes); a window posted
// again is a duplicate and counted once, so the edge retries freely and posts its queue after an
// outage under the same keys. A rejected window (unknown publisher, bad shape) will never be
// taken: the edge drops it.
package edge

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
)

// Headers of a signed edge request.
const (
	HeaderTimestamp = "X-Maktaba-Edge-Timestamp"
	HeaderSignature = "X-Maktaba-Edge-Signature"
)

// MaxSkew is how far an edge request's timestamp may be from the cloud's clock.
const MaxSkew = 5 * time.Minute

// MaxWindows bounds one post (the edge sends a publisher's queue in pages).
const MaxWindows = 2000

// Deps are the routes' dependencies.
type Deps struct {
	Store  *billingpkg.BroadcastStore
	Key    ed25519.PublicKey // nil: 503
	Logger *slog.Logger
	Now    func() time.Time
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Mount registers the edge routes (no user token: the edge signs each request).
func Mount(r chi.Router, d *Deps) {
	r.Post("/_edge/usage", d.Usage)
	r.Get("/_edge/publishers/{id}", d.Publisher)
}

// SignedPayload is the bytes an edge request's signature covers.
func SignedPayload(ts, method, path string, body []byte) []byte {
	var b bytes.Buffer
	b.WriteString(ts)
	b.WriteByte('\n')
	b.WriteString(method)
	b.WriteByte(' ')
	b.WriteString(path)
	b.WriteByte('\n')
	b.Write(body)
	return b.Bytes()
}

// Sign signs an edge request (the rehearsal's and the tests' fake edge; the Worker does the same
// with WebCrypto).
func Sign(priv ed25519.PrivateKey, req *http.Request, body []byte, at time.Time) {
	ts := strconv.FormatInt(at.Unix(), 10)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, SignedPayload(ts, req.Method, req.URL.Path, body))))
}

var errBadSignature = errors.New("edge: bad signature")

// verify reads the body and checks the request's signature and freshness.
func (d *Deps) verify(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	if d.Key == nil || d.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "edge_not_configured", "")
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "")
		return nil, false
	}
	if err := d.check(r, body); err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_edge_signature", "")
		return nil, false
	}
	return body, true
}

func (d *Deps) check(r *http.Request, body []byte) error {
	ts := r.Header.Get(HeaderTimestamp)
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errBadSignature
	}
	if skew := d.now().Sub(time.Unix(sec, 0)); skew > MaxSkew || skew < -MaxSkew {
		return errBadSignature
	}
	sig, err := base64.StdEncoding.DecodeString(r.Header.Get(HeaderSignature))
	if err != nil || !ed25519.Verify(d.Key, SignedPayload(ts, r.Method, r.URL.Path, body), sig) {
		return errBadSignature
	}
	return nil
}

// UsagePost is the edge's usage post.
type UsagePost struct {
	Edge    string                   `json:"edge"`
	Windows []billingpkg.UsageWindow `json:"windows"`
}

// Rejected is a window that will never be taken.
type Rejected struct {
	Key   string `json:"key"`
	Error string `json:"error"`
}

// UsageReply is the cloud's answer.
type UsageReply struct {
	Taken      []string                    `json:"taken"`
	Duplicates []string                    `json:"duplicates"`
	Rejected   []Rejected                  `json:"rejected"`
	States     []billingpkg.PublisherState `json:"states"`
}

// Usage takes the edge's windows, each once, and answers with each publisher's state.
func (d *Deps) Usage(w http.ResponseWriter, r *http.Request) {
	body, ok := d.verify(w, r, 1<<20)
	if !ok {
		return
	}
	var post UsagePost
	if err := json.Unmarshal(body, &post); err != nil || len(post.Windows) > MaxWindows {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {edge, windows[≤2000]}")
		return
	}
	out := UsageReply{Taken: []string{}, Duplicates: []string{}, Rejected: []Rejected{}, States: []billingpkg.PublisherState{}}
	seen := map[string]bool{}
	var publishers []string
	for _, win := range post.Windows {
		key := billingpkg.WindowKey(win.Publisher, win.Window)
		dup, err := d.Store.RecordWindow(r.Context(), win)
		switch {
		case errors.Is(err, billingpkg.ErrUnknownPublisher):
			out.Rejected = append(out.Rejected, Rejected{key, "unknown_publisher"})
			continue
		case errors.Is(err, billingpkg.ErrBadWindow):
			out.Rejected = append(out.Rejected, Rejected{key, "bad_window"})
			continue
		case err != nil:
			// the rest stay with the edge, posted again under the same keys (taken ones count once)
			d.log().Error("edge: usage window", "key", key, "err", err)
			writeErr(w, http.StatusServiceUnavailable, "try_again", "")
			return
		case dup:
			out.Duplicates = append(out.Duplicates, key)
		default:
			out.Taken = append(out.Taken, key)
		}
		if !seen[win.Publisher] {
			seen[win.Publisher] = true
			publishers = append(publishers, win.Publisher)
		}
	}
	// each publisher's state after all of its windows
	for _, id := range publishers {
		st, err := d.Store.State(r.Context(), id)
		if err != nil {
			d.log().Error("edge: publisher state", "publisher", id, "err", err)
			continue
		}
		out.States = append(out.States, st)
	}
	writeJSON(w, http.StatusOK, out)
}

// Publisher is one publisher's state.
func (d *Deps) Publisher(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.verify(w, r, 0); !ok {
		return
	}
	st, err := d.Store.State(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, billingpkg.ErrUnknownPublisher) {
		writeErr(w, http.StatusNotFound, "unknown_publisher", "")
		return
	}
	if err != nil {
		d.log().Error("edge: publisher state", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (d *Deps) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
