// Package edge (handlers) is what the broadcast edge (story 50.21's multi-tenant Worker) calls
// on the cloud API (story 50.20 §3). The contract, its signature and its wire types are
// internal/broadcast's (edge.go), shared with the edge through testdata/vectors.json. Every
// request is signed with the edge's Ed25519 key; the cloud holds only the public half
// (broadcast.edge_public_key). Without it, every route is 503.
//
// Signing: the edge sends
//
//	X-Maktaba-Edge-Time:      <unix ms>
//	X-Maktaba-Edge-Signature: base64url(ed25519("maktaba-broadcast-edge-v1\n<METHOD>\n<path>\n<ms>\n<hex sha256 body>"))
//
// and the cloud refuses a time more than 5 minutes from its clock (401 bad_edge_signature).
//
//	POST /_edge/usage                 broadcast.UsagePost {"edge", "windows": [...]} (at most 2,000)
//	                                  → 200 broadcast.UsageReply {"taken": [key], "duplicates": [key],
//	                                         "rejected": [{"key", "error"}], "states": [PublisherState],
//	                                         "plans": [Plan]}
//	GET  /_edge/publishers/{id}       → 200 PublisherState
//
// A window's key is <publisher>:<window_start> (RFC 3339 UTC, whole seconds); a window posted
// again is a duplicate and counted once, so the edge retries freely and posts its queue after an
// outage under the same keys. Every key in a reply is the posted window's own. A rejected window
// (unknown publisher, a key, end or counter that doesn't match its start) will never be taken:
// the edge drops it.
package edge

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

// MaxWindows bounds one post (the edge sends a publisher's queue in pages).
const MaxWindows = broadcast.MaxUsageWindows

// MaxBody bounds a usage post's bytes: 2,000 windows with every counter at its largest fit.
const MaxBody = 2 << 20

// Store is the publishers' usage and plan state (billing.BroadcastStore).
type Store interface {
	RecordWindow(ctx context.Context, w billingpkg.UsageWindow) (dup bool, err error)
	State(ctx context.Context, serverID string) (billingpkg.PublisherState, error)
}

// Deps are the routes' dependencies.
type Deps struct {
	Store  Store
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
	if err := broadcast.VerifyEdgeRequest(d.Key, r.Method, r.URL.Path, r.Header, body, d.now()); err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_edge_signature", "")
		return nil, false
	}
	return body, true
}

var errDuplicate = errors.New("edge: duplicate window")

// Usage takes the edge's windows, each once, and answers with each publisher's state.
func (d *Deps) Usage(w http.ResponseWriter, r *http.Request) {
	body, ok := d.verify(w, r, MaxBody)
	if !ok {
		return
	}
	var post broadcast.UsagePost
	if err := json.Unmarshal(body, &post); err != nil || len(post.Windows) > MaxWindows {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {edge, windows[≤2000]}")
		return
	}
	out := broadcast.UsageReply{Taken: []string{}, Duplicates: []string{}, Rejected: []broadcast.Rejected{},
		States: []billingpkg.PublisherState{}, Plans: []broadcast.Plan{}}
	seen := map[string]bool{}
	var publishers []string
	for _, posted := range post.Windows {
		key := posted.Key
		win, err := posted.Billing()
		if err == nil {
			var dup bool
			dup, err = d.Store.RecordWindow(r.Context(), win)
			if err == nil && dup {
				err = errDuplicate
			}
		}
		switch {
		case errors.Is(err, billingpkg.ErrUnknownPublisher):
			out.Rejected = append(out.Rejected, broadcast.Rejected{Key: key, Error: "unknown_publisher"})
			continue
		case errors.Is(err, billingpkg.ErrBadWindow), errors.Is(err, broadcast.ErrWindowShape):
			out.Rejected = append(out.Rejected, broadcast.Rejected{Key: key, Error: "bad_window"})
			continue
		case errors.Is(err, errDuplicate):
			out.Duplicates = append(out.Duplicates, key)
		case err != nil:
			// the rest stay with the edge, posted again under the same keys (taken ones count once)
			d.log().Error("edge: usage window", "key", key, "err", err)
			writeErr(w, http.StatusServiceUnavailable, "try_again", "")
			return
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
		out.Plans = append(out.Plans, broadcast.PlanFromState(st))
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
