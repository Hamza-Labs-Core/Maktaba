// Package broadcastgate closes the cloud's broadcast and moderation routes while home
// broadcasting is off (broadcast.enabled = false).
//
// One middleware on the root of the user listener and of the operator console's, before any
// route, sign-in or rate limit: while broadcasting is off, every path under Paths answers what an
// unknown path answers (http.NotFound), whatever the method, the credential or the id in it, so
// nothing is learned about a home, a channel or the route itself. The paths decide, not the
// handlers: a route added under one of them is closed without doing anything. A route table test
// (cmd/maktaba-cloud) fails on a broadcast or moderation handler mounted anywhere else.
//
// What stays open, and to whom, is the table in exceptions:
//
//   - the edge's signed pull of the whole revocation list, and the kill switch on the console:
//     both only ever take access away or undo that. A test publisher's home pulls its own copy
//     (its own entries and the ones that name no home): it refuses to publish without a fresh
//     one, and stops when one names it;
//   - the operator's grant that makes a home a test publisher (POST /v1/admin/servers/{id}/broadcast);
//   - what a test publisher's rehearsal needs, for that home's own credential, its owner's
//     acceptance of the agreement, and the edge's signed calls about it.
//
// A marked home's routes are asked for in this order: the mark, then the secret. The secret's
// check is slow on purpose (argon2id), so asking for it first would tell by its time which ids
// are homes. An error is a 404 too: the gate fails closed.
package broadcastgate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// Paths are the prefixes of every broadcast and moderation route on either listener.
var Paths = []string{
	"/v1/broadcast",
	"/v1/billing/broadcast",
	"/v1/moderation",
	"/v1/admin/broadcast",
	"/v1/admin/moderation",
	"/_edge",
}

// serverBroadcast is the console's routes about one home's broadcasting.
var serverBroadcast = regexp.MustCompile(`^/v1/admin/servers/[^/]*/broadcast`)

// Covers says whether path is a broadcast or moderation path.
func Covers(path string) bool {
	for _, p := range Paths {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return serverBroadcast.MatchString(path)
}

// Who an exception is for.
type Who string

const (
	// Home is a test publisher's own credential (server id and secret).
	Home Who = "home"
	// Owner is the signed-in account that owns a test publisher.
	Owner Who = "owner"
	// Edge is the broadcast edge's signature.
	Edge Who = "edge"
	// EdgeHome is the edge's signature about a test publisher, the path's id.
	EdgeHome Who = "edge, about a test publisher"
	// EdgeOrHome is either of them.
	EdgeOrHome Who = "edge or home"
	// Staff is the operator console: its own gate (service admin, the second factor) decides.
	Staff Who = "staff"
)

// Exception is a route that answers while broadcasting is off, and to whom.
type Exception struct {
	Method string
	// Path is a chi pattern; {x} is one segment.
	Path string
	Who  Who
	// Why names what needs it.
	Why string
	re  *regexp.Regexp
}

// Exceptions lists them (a copy).
func Exceptions() []Exception { return append([]Exception(nil), exceptions...) }

var exceptions = compile([]Exception{
	// the kill switch
	{Method: http.MethodGet, Path: "/v1/broadcast/revocations", Who: EdgeOrHome, Why: "the kill switch's signed list: whole for the edge, a home's own copy for a test publisher, which can't publish without it"},
	{Method: http.MethodGet, Path: "/v1/admin/broadcast/revocations", Who: Staff, Why: "the kill switch"},
	{Method: http.MethodPost, Path: "/v1/admin/broadcast/revocations", Who: Staff, Why: "the kill switch"},
	{Method: http.MethodPost, Path: "/v1/admin/broadcast/revocations/{id}/lift", Who: Staff, Why: "undo a kill switch entry"},
	// the mark itself
	{Method: http.MethodPost, Path: "/v1/admin/servers/{id}/broadcast", Who: Staff, Why: "mark or unmark a test publisher"},
	// a test publisher's rehearsal: accept the agreement, publish, get a token, invite a viewer
	{Method: http.MethodGet, Path: "/v1/broadcast/agreement", Who: Owner, Why: "publishing needs the agreement"},
	{Method: http.MethodPost, Path: "/v1/broadcast/agreement", Who: Owner, Why: "publishing needs the agreement"},
	{Method: http.MethodGet, Path: "/v1/broadcast/state", Who: Home, Why: "the home's publish checks"},
	{Method: http.MethodPost, Path: "/v1/broadcast/channels", Who: Home, Why: "publish"},
	{Method: http.MethodDelete, Path: "/v1/broadcast/channels/{id}", Who: Home, Why: "unpublish"},
	{Method: http.MethodPost, Path: "/v1/broadcast/ingest-token", Who: Home, Why: "upload and the speed test"},
	{Method: http.MethodGet, Path: "/v1/broadcast/access", Who: Home, Why: "viewers come by invitation"},
	{Method: http.MethodPut, Path: "/v1/broadcast/channels/{id}/access", Who: Home, Why: "viewers come by invitation"},
	{Method: http.MethodPut, Path: "/v1/broadcast/viewer-limits", Who: Home, Why: "the viewer limits"},
	{Method: http.MethodGet, Path: "/v1/broadcast/invites", Who: Home, Why: "viewers come by invitation"},
	{Method: http.MethodPost, Path: "/v1/broadcast/invites", Who: Home, Why: "viewers come by invitation"},
	{Method: http.MethodDelete, Path: "/v1/broadcast/invites/{id}", Who: Home, Why: "withdraw an invite"},
	{Method: http.MethodGet, Path: "/v1/broadcast/invites/{id}/devices", Who: Home, Why: "an invite's devices"},
	{Method: http.MethodDelete, Path: "/v1/broadcast/invites/{id}/devices/{pass}", Who: Home, Why: "withdraw a device"},
	// the edge: how a test publisher's viewers are counted and which mode new ones get. A window
	// about any other home is rejected as an unknown publisher (EdgeStore).
	{Method: http.MethodPost, Path: bc.UsagePath, Who: Edge, Why: "a test publisher's usage"},
	{Method: http.MethodGet, Path: "/_edge/publishers/{id}", Who: EdgeHome, Why: "a test publisher's state"},
})

var paramRE = regexp.MustCompile(`\\\{[a-z]+\\\}`)

func compile(in []Exception) []Exception {
	for i := range in {
		in[i].re = regexp.MustCompile("^" + paramRE.ReplaceAllString(regexp.QuoteMeta(in[i].Path), `([^/]+)`) + "$")
	}
	return in
}

// MaxEdgeBody bounds a signed edge post read here (the usage post's own limit).
const MaxEdgeBody = 2 << 20

// Agents checks a home's credential (stores.Servers).
type Agents interface {
	VerifySecret(ctx context.Context, serverID, secret string) error
}

// Publishers answers who is a test publisher (DBPublishers).
type Publishers interface {
	// IsTest: serverID is a home a service admin marked.
	IsTest(ctx context.Context, serverID string) (bool, error)
	// OwnsTest: userID owns such a home.
	OwnsTest(ctx context.Context, userID string) (bool, error)
}

// DBPublishers reads servers.broadcast_test_publisher.
type DBPublishers struct{ DB *sql.DB }

// IsTest implements Publishers.
func (d DBPublishers) IsTest(ctx context.Context, serverID string) (bool, error) {
	if !billingpkg.IsUUID(serverID) {
		return false, nil
	}
	return d.one(ctx, `SELECT 1 FROM servers WHERE id = $1 AND broadcast_test_publisher`, serverID)
}

// OwnsTest implements Publishers.
func (d DBPublishers) OwnsTest(ctx context.Context, userID string) (bool, error) {
	if !billingpkg.IsUUID(userID) {
		return false, nil
	}
	return d.one(ctx, `SELECT 1 FROM servers WHERE owner_user_id = $1 AND broadcast_test_publisher LIMIT 1`, userID)
}

func (d DBPublishers) one(ctx context.Context, q, arg string) (bool, error) {
	var one int
	err := d.DB.QueryRowContext(ctx, q, arg).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Gate is the middleware's wiring. A nil field closes the exceptions that need it.
type Gate struct {
	// Enabled is broadcast.enabled: on, the middleware does nothing.
	Enabled    bool
	Publishers Publishers
	Agents     Agents
	// EdgeKey is broadcast.edge_public_key.
	EdgeKey ed25519.PublicKey
	// User is the account sign-in (middleware.RequireUser).
	User func(http.Handler) http.Handler
	// Console: this is the operator console's listener, the only one with staff routes.
	Console bool
	Now     func() time.Time
}

// Middleware goes on the root router, before its routes.
func (g *Gate) Middleware(next http.Handler) http.Handler {
	if g.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// chi routes by RawPath when there is one: neither spelling may be a broadcast path
		if !Covers(r.URL.Path) && !Covers(r.URL.RawPath) {
			next.ServeHTTP(w, r)
			return
		}
		for _, e := range exceptions {
			if e.Method != r.Method || r.URL.RawPath != "" {
				continue
			}
			if m := e.re.FindStringSubmatch(r.URL.Path); m != nil && g.admits(e, m, r) {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
}

func (g *Gate) admits(e Exception, m []string, r *http.Request) bool {
	switch e.Who {
	case Staff:
		return g.Console
	case Home:
		return g.home(r)
	case Owner:
		return g.owner(r)
	case Edge:
		return g.edge(r)
	case EdgeHome:
		return g.edge(r) && g.isTest(r.Context(), m[1])
	case EdgeOrHome:
		if r.Header.Get(bc.HeaderEdgeSignature) != "" {
			return g.edge(r)
		}
		return g.home(r)
	}
	return false
}

func (g *Gate) isTest(ctx context.Context, serverID string) bool {
	if g.Publishers == nil {
		return false
	}
	ok, err := g.Publishers.IsTest(ctx, serverID)
	return err == nil && ok
}

func (g *Gate) home(r *http.Request) bool {
	id, secret, ok := r.BasicAuth()
	return ok && g.Agents != nil && g.isTest(r.Context(), id) && g.Agents.VerifySecret(r.Context(), id, secret) == nil
}

// owner asks the account sign-in with an answer nobody sees.
func (g *Gate) owner(r *http.Request) bool {
	if g.User == nil || g.Publishers == nil {
		return false
	}
	ok := false
	g.User(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		owns, err := g.Publishers.OwnsTest(r.Context(), middleware.GetUserID(r.Context()))
		ok = err == nil && owns
	})).ServeHTTP(discard{h: http.Header{}}, r)
	return ok
}

// edge checks the edge's signature and puts the body back.
func (g *Gate) edge(r *http.Request) bool {
	if g.EdgeKey == nil || r.Header.Get(bc.HeaderEdgeSignature) == "" {
		return false
	}
	var body []byte
	if r.Body != nil {
		var err error
		if body, err = io.ReadAll(io.LimitReader(r.Body, MaxEdgeBody+1)); err != nil || len(body) > MaxEdgeBody {
			return false
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	now := time.Now()
	if g.Now != nil {
		now = g.Now()
	}
	// a GET is signed without a body
	return (r.Method != http.MethodGet || len(body) == 0) &&
		bc.VerifyEdgeRequest(g.EdgeKey, r.Method, r.URL.Path, r.Header, body, now) == nil
}

type discard struct{ h http.Header }

func (d discard) Header() http.Header         { return d.h }
func (d discard) Write(b []byte) (int, error) { return len(b), nil }
func (d discard) WriteHeader(int)             {}

// edgeStore is the usage intake and the publishers' states (billing.BroadcastStore).
type edgeStore interface {
	RecordWindow(ctx context.Context, w billingpkg.UsageWindow) (bool, error)
	State(ctx context.Context, serverID string) (billingpkg.PublisherState, error)
}

// EdgeStore answers the edge about test publishers only while broadcasting is off: any other
// publisher is unknown, so the edge drops its windows and learns nothing.
type EdgeStore struct {
	Store      edgeStore
	Enabled    bool
	Publishers Publishers
}

func (s EdgeStore) known(ctx context.Context, serverID string) error {
	if s.Enabled {
		return nil
	}
	ok, err := s.Publishers.IsTest(ctx, serverID)
	if err == nil && !ok {
		err = billingpkg.ErrUnknownPublisher
	}
	return err
}

// RecordWindow implements the edge handlers' Store.
func (s EdgeStore) RecordWindow(ctx context.Context, w billingpkg.UsageWindow) (bool, error) {
	if err := s.known(ctx, w.Publisher); err != nil {
		return false, err
	}
	return s.Store.RecordWindow(ctx, w)
}

// State implements the edge handlers' Store.
func (s EdgeStore) State(ctx context.Context, serverID string) (billingpkg.PublisherState, error) {
	if err := s.known(ctx, serverID); err != nil {
		return billingpkg.PublisherState{}, err
	}
	return s.Store.State(ctx, serverID)
}
