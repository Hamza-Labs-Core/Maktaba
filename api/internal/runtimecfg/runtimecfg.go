// Package runtimecfg reads the runtime settings an admin PATCHes through
// /api/settings (app_settings rows such as search.fts_weight or
// session.url_ttl_sec) for the handlers that use them. A value that is not
// stored, or not a usable number, reads as unset and the caller keeps its own
// default: the behaviour without the row is the behaviour before it.
//
// Reads are cached for a few seconds so a hot path (search, every SSE
// connection) costs no query per request; the settings handler calls
// Invalidate after a PATCH so the API that took the change uses it at once.
package runtimecfg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The runtime keys (the settings handler's PATCH allowlist).
const (
	SearchFTSWeight      = "search.fts_weight"
	SearchSemanticWeight = "search.semantic_weight"
	RecsCacheTTL         = "recs.cache_ttl_sec"
	SessionURLTTL        = "session.url_ttl_sec"
	PauseGrace           = "pause.grace_sec"
	WSHeartbeat          = "ws.heartbeat_interval_sec"
	OpenRegistration     = "auth.open_registration"
	// GraphQLPersistedOnly turns on persisted-query allowlist mode (Story
	// 7.17 AC-5): non-admins run only stored GraphQL operations. Default off.
	GraphQLPersistedOnly = "graphql.persisted_only"
	// HomeName is the home's name for people (every signed-in user's app shows
	// it); unset or empty, the computer's name.
	HomeName = "home.name"
)

// DefaultTTL is how long a read is cached.
const DefaultTTL = 5 * time.Second

// Reader reads runtime settings. A nil *Reader reads every key as unset.
type Reader struct {
	DB  *sql.DB
	TTL time.Duration
	Now func() time.Time

	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	raw []byte
	ok  bool
	at  time.Time
}

// New is a reader over db.
func New(db *sql.DB) *Reader { return &Reader{DB: db} }

func (r *Reader) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reader) ttl() time.Duration {
	if r.TTL > 0 {
		return r.TTL
	}
	return DefaultTTL
}

// Invalidate drops the cache (after a PATCH).
func (r *Reader) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.cache = nil
	r.mu.Unlock()
}

func (r *Reader) raw(ctx context.Context, key string) ([]byte, bool) {
	if r == nil || r.DB == nil {
		return nil, false
	}
	now := r.now()
	r.mu.Lock()
	if e, hit := r.cache[key]; hit && now.Sub(e.at) < r.ttl() {
		r.mu.Unlock()
		return e.raw, e.ok
	}
	r.mu.Unlock()
	var v []byte
	err := r.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	ok := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false // a failed read is not cached: the next one retries
	}
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]entry{}
	}
	r.cache[key] = entry{raw: v, ok: ok, at: now}
	r.mu.Unlock()
	return v, ok
}

// Number parses a stored setting value: a JSON number, or a JSON string or
// bare text holding one.
func Number(raw []byte) (float64, bool) {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

// Float is key's value when it is a number >= 0.
func (r *Reader) Float(ctx context.Context, key string) (float64, bool) {
	raw, ok := r.raw(ctx, key)
	if !ok {
		return 0, false
	}
	f, ok := Number(raw)
	if !ok || f < 0 {
		return 0, false
	}
	return f, true
}

// Seconds is key's value as a duration when it is a number of seconds > 0.
func (r *Reader) Seconds(ctx context.Context, key string) (time.Duration, bool) {
	f, ok := r.Float(ctx, key)
	if !ok || f <= 0 {
		return 0, false
	}
	return time.Duration(f * float64(time.Second)), true
}

// Bool reads a boolean setting (a JSON true/false, or the strings
// "true"/"false"). Unset or unreadable reads as (false, false).
func (r *Reader) Bool(ctx context.Context, key string) (bool, bool) {
	raw, ok := r.raw(ctx, key)
	if !ok {
		return false, false
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// Text reads a text setting (a JSON string, or bare text), trimmed. Unset,
// unreadable or empty reads as ("", false).
func (r *Reader) Text(ctx context.Context, key string) (string, bool) {
	raw, ok := r.raw(ctx, key)
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	s = strings.TrimSpace(s)
	return s, s != ""
}
