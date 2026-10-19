package graphql

// Persisted queries (Story 7.17 AC-5), the Apollo protocol: a client
// sends the sha256 of an operation instead of its text, either as
// `extensions.persistedQuery.sha256Hash` (Apollo's APQ) or as
// `persistedQueryId`. A hash the store holds runs that operation; an
// unknown one answers PersistedQueryNotFound (HTTP 200, the Apollo shape)
// and the client retries with the text plus the hash, which registers it.
//
// Allowlist mode (the runtime setting graphql.persisted_only, default
// off): the stored operations are the allowlist. A non-admin may then run
// only a stored operation, by hash or by text, and only an admin
// registers new ones. Off, any caller registers (the store is a cache
// keyed by content, so a registration cannot change what a hash means).

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Apollo's error message and code for an unknown hash (clients match on
// them to retry with the full text).
const (
	PersistedQueryNotFound     = "PersistedQueryNotFound"
	CodePersistedNotFound      = "PERSISTED_QUERY_NOT_FOUND"
	CodePersistedHashMismatch  = "PERSISTED_QUERY_HASH_MISMATCH"
	CodePersistedNotAllowed    = "PERSISTED_QUERY_NOT_ALLOWED"
	persistedCacheMax          = 2048
	persistedProtocolVersion   = 1
	persistedHashHexCharacters = 64
)

// PersistedStore holds the persisted operations: the
// graphql_persisted_queries table (slot 0140) behind a bounded in-memory
// cache. A nil store (or nil DB) holds nothing.
type PersistedStore struct {
	DB *sql.DB

	mu    sync.RWMutex
	cache map[string]string
}

// NewPersistedStore returns the store over db.
func NewPersistedStore(db *sql.DB) *PersistedStore {
	return &PersistedStore{DB: db, cache: map[string]string{}}
}

// QueryHash is the lowercase hex sha256 of an operation's text, the id a
// client sends.
func QueryHash(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:])
}

// Lookup returns the operation stored under hash.
func (s *PersistedStore) Lookup(ctx context.Context, hash string) (string, bool, error) {
	if s == nil || s.DB == nil {
		return "", false, nil
	}
	s.mu.RLock()
	src, ok := s.cache[hash]
	s.mu.RUnlock()
	if ok {
		return src, true, nil
	}
	err := s.DB.QueryRowContext(ctx, `SELECT query FROM graphql_persisted_queries WHERE sha256 = $1`, hash).Scan(&src)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	s.remember(hash, src)
	return src, true, nil
}

// Register stores query under its own hash (idempotent).
func (s *PersistedStore) Register(ctx context.Context, query, userID string) error {
	if s == nil || s.DB == nil {
		return nil
	}
	hash := QueryHash(query)
	var by any
	if _, err := uuid.Parse(userID); err == nil {
		by = userID
	}
	if _, err := s.DB.ExecContext(ctx, `
		INSERT INTO graphql_persisted_queries (sha256, query, created_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (sha256) DO NOTHING
	`, hash, query, by); err != nil {
		return err
	}
	s.remember(hash, query)
	return nil
}

func (s *PersistedStore) remember(hash, src string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil || len(s.cache) >= persistedCacheMax {
		s.cache = map[string]string{}
	}
	s.cache[hash] = src
}

// persistedExt is the Apollo request extension.
type persistedExt struct {
	PersistedQuery *struct {
		Version    int    `json:"version"`
		Sha256Hash string `json:"sha256Hash"`
	} `json:"persistedQuery"`
}

// requestedHash is the hash the request names ("" for none).
func (req *gqlRequest) requestedHash() string {
	if req.Extensions.PersistedQuery != nil && req.Extensions.PersistedQuery.Sha256Hash != "" {
		return strings.ToLower(strings.TrimSpace(req.Extensions.PersistedQuery.Sha256Hash))
	}
	return strings.ToLower(strings.TrimSpace(req.PersistedQueryID))
}

func validHash(h string) bool {
	if len(h) != persistedHashHexCharacters {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

// persistedOnly reports whether allowlist mode is on.
func (h *Handler) persistedOnly(ctx context.Context) bool {
	return h.PersistedOnly != nil && h.PersistedOnly(ctx)
}

// resolvePersisted fills req.Query from the store when the request names
// only a hash, and applies allowlist mode. It returns register=true when
// the text should be stored once it has validated (a hash + text retry).
// A non-nil errs is the response, with its HTTP status.
func (h *Handler) resolvePersisted(ctx context.Context, req *gqlRequest) (register bool, status int, errs []map[string]any) {
	p := principal.FromContext(ctx)
	isAdmin := p != nil && p.IsAdmin
	// allowlist mode never limits an admin, so an admin's request does not
	// read the setting
	only := !isAdmin && h.persistedOnly(ctx)
	hash := req.requestedHash()
	if ext := req.Extensions.PersistedQuery; ext != nil && ext.Version != 0 && ext.Version != persistedProtocolVersion {
		return false, http.StatusBadRequest, []map[string]any{gqlErr("unsupported persisted query version", "bad-request")}
	}
	if hash != "" && !validHash(hash) {
		return false, http.StatusBadRequest, []map[string]any{gqlErr("persisted query id must be a hex sha256", "bad-request")}
	}

	if strings.TrimSpace(req.Query) == "" {
		if hash == "" {
			return false, http.StatusBadRequest, []map[string]any{
				gqlErr("malformed GraphQL request body: want {query, operationName, variables}", "bad-request")}
		}
		src, ok, err := h.Persisted.Lookup(ctx, hash)
		if err != nil {
			return false, http.StatusServiceUnavailable, []map[string]any{gqlErr("persisted query store unavailable", "unavailable")}
		}
		if !ok {
			// Apollo: HTTP 200 + this exact message/code, the client
			// retries with the text.
			return false, http.StatusOK, []map[string]any{gqlErr(PersistedQueryNotFound, CodePersistedNotFound)}
		}
		req.Query = src
		return false, 0, nil
	}

	sum := QueryHash(req.Query)
	if hash != "" && hash != sum {
		return false, http.StatusBadRequest, []map[string]any{gqlErr("provided sha does not match query", CodePersistedHashMismatch)}
	}
	if only {
		if _, ok, err := h.Persisted.Lookup(ctx, sum); err != nil {
			return false, http.StatusServiceUnavailable, []map[string]any{gqlErr("persisted query store unavailable", "unavailable")}
		} else if !ok {
			return false, http.StatusForbidden, []map[string]any{
				gqlErr("only persisted queries may run on this server (graphql.persisted_only)", CodePersistedNotAllowed)}
		}
		return false, 0, nil
	}
	return hash != "" && h.Persisted != nil, 0, nil
}

// registerPersisted stores a validated operation (best effort).
func (h *Handler) registerPersisted(ctx context.Context, query string) {
	userID := ""
	if p := principal.FromContext(ctx); p != nil {
		userID = p.UserID
	}
	_ = h.Persisted.Register(ctx, query, userID)
}
