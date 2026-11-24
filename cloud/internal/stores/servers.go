package stores

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Server mirrors the `servers` row.
type Server struct {
	ID          string
	OwnerUserID string
	Name        string
	Slug        string
	Plan        string
	Version     sql.NullString
	LastSeenAt  sql.NullTime
	DirectIP    sql.NullString
	DirectPort  sql.NullInt64
	CreatedAt   time.Time
	// ExtraStreams are the relayed streams the owner bought beyond the plan's (story 50.19 §6).
	ExtraStreams int
}

var (
	ErrServerNotFound = errors.New("servers: not found")
	ErrClaimInvalid   = errors.New("servers: claim token invalid or expired")
)

// Servers is the SQL access layer for `servers`, `server_claims`,
// `server_health`.
type Servers struct {
	DB *sql.DB
}

func NewServers(db *sql.DB) *Servers { return &Servers{DB: db} }

// MintClaim issues a fresh 8-char base32 code with a 10-minute TTL.
// Returns the code (to show the user) and the row id (for audit).
//
// We store the code hashed at rest so a DB leak can't be replayed to
// claim someone's server.
func (s *Servers) MintClaim(ctx context.Context, userID string) (code string, err error) {
	buf := make([]byte, 5) // 5 bytes → 8 base32 chars (RFC 4648 no padding).
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	code = strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))
	if len(code) > 8 {
		code = code[:8]
	}
	hash := hashClaim(code)
	_, err = s.DB.ExecContext(ctx, `
        INSERT INTO server_claims (token_hash, code, user_id, expires_at)
        VALUES ($1, $2, $3, now() + INTERVAL '10 minutes')
    `, hash, code, userID)
	return code, err
}

// ErrSlugTaken: another server has the slug.
var ErrSlugTaken = errors.New("servers: slug taken")

// ErrSlugReserved: the slug is on the reserved list (operator and infrastructure names such
// as api, canary, r1…r99: docs/cloud-dns.md, story 50.01), so no home can take it.
var ErrSlugReserved = errors.New("servers: slug reserved")

// RedeemClaim consumes an 8-char claim code and registers the server it
// links, in one transaction: the code is spent only when the server is
// created (a taken slug leaves it usable for another try), and two
// agents racing the same code cannot both win (the claim's row is locked
// and its used_at read under that lock). A code whose owner is not
// active (suspended) is refused as an invalid one is.
//
// retryKey is the agent's own secret for this claim (empty: an agent that sends none). An agent
// whose first answer was lost redeems again with the same key, within the claim's lifetime, and
// gets the same server. The caller derives the secret from the key, so every answer carries the
// same one and all of them work, two at once included; isSecret says whether a stored hash is
// already that secret's, and only when it is not (the cloud's own key changed in between) does
// the server get secretHash. It
// holds only until the home has used its secret (VerifyHome): from then on the claim is spent
// for everyone.
func (s *Servers) RedeemClaim(ctx context.Context, code, name, slug, secretHash, version string, pubKey []byte, retryKey string, isSecret func(hash string) bool) (Server, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// whose code it is, without locking it: the account is locked first, then its rows (the
	// order LockAccount's writers and the account purge keep)
	var uid string
	err = tx.QueryRowContext(ctx, `
        SELECT user_id FROM server_claims WHERE token_hash = $1 AND expires_at > now()
    `, hashClaim(code)).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrClaimInvalid
	}
	if err != nil {
		return Server{}, err
	}
	// The home takes the plan its owner has at this moment, and no plan change is under way: one
	// that began first has committed, one that begins now waits and then updates this home too.
	status, _, err := LockAccount(ctx, tx, uid)
	if errors.Is(err, ErrUserNotFound) || (err == nil && status != StatusActive) {
		return Server{}, ErrClaimInvalid
	}
	if err != nil {
		return Server{}, err
	}
	var used bool
	var usedServer, retryHash sql.NullString
	err = tx.QueryRowContext(ctx, `
        SELECT used_at IS NOT NULL, used_server_id::text, retry_key_hash FROM server_claims
         WHERE token_hash = $1 AND expires_at > now() FOR UPDATE
    `, hashClaim(code)).Scan(&used, &usedServer, &retryHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrClaimInvalid
	}
	if err != nil {
		return Server{}, err
	}
	keyHash := hashRetryKey(retryKey)
	if used {
		if !keyHash.Valid || !retryHash.Valid || !usedServer.Valid ||
			subtle.ConstantTimeCompare([]byte(keyHash.String), []byte(retryHash.String)) != 1 {
			return Server{}, ErrClaimInvalid
		}
		return redeemAgain(ctx, tx, usedServer.String, uid, secretHash, isSecret)
	}
	if _, err := tx.ExecContext(ctx, `
        UPDATE server_claims SET used_at = now(), retry_key_hash = $2 WHERE token_hash = $1
    `, hashClaim(code), keyHash); err != nil {
		return Server{}, err
	}
	var reserved int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM reserved_slugs WHERE slug = $1`, slug).Scan(&reserved); err != nil {
		return Server{}, err
	}
	if reserved > 0 {
		return Server{}, ErrSlugReserved
	}
	// The new home takes its owner's plan and extra streams, as a plan change gives them to the
	// homes the account already has (SetEffectivePlan, setExtraStreams): the column defaults
	// (free, 0) would refuse the relay to a home claimed after its owner subscribed.
	var sv Server
	err = tx.QueryRowContext(ctx, `
        INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, version, public_key, plan, extra_streams)
        SELECT $1, $2, $3, $4, $5, $6, u.plan, COALESCE(sub.extra_streams, 0)
          FROM users u LEFT JOIN subscriptions sub ON sub.user_id = u.id
         WHERE u.id = $1
        ON CONFLICT (slug) DO NOTHING
        RETURNING id, owner_user_id, name, slug, plan, version, last_seen_at, direct_ip::text, direct_port, created_at, extra_streams
    `, uid, name, slug, secretHash, sql.NullString{String: version, Valid: version != ""}, pubKey).Scan(
		&sv.ID, &sv.OwnerUserID, &sv.Name, &sv.Slug, &sv.Plan, &sv.Version, &sv.LastSeenAt, &sv.DirectIP, &sv.DirectPort, &sv.CreatedAt, &sv.ExtraStreams,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrSlugTaken
	}
	if err != nil {
		return Server{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE server_claims SET used_server_id = $1 WHERE token_hash = $2`, sv.ID, hashClaim(code)); err != nil {
		return Server{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO subdomains (slug, server_id) VALUES ($1, $2) ON CONFLICT (slug) DO NOTHING`, sv.Slug, sv.ID); err != nil {
		return Server{}, err
	}
	return sv, tx.Commit()
}

// redeemAgain answers a claim already made, while the home has not used its secret: its use
// clears the claim's retry key (endClaimRetry), and the caller holds the claim's row. It commits
// tx.
func redeemAgain(ctx context.Context, tx *sql.Tx, serverID, ownerID, secretHash string, isSecret func(hash string) bool) (Server, error) {
	var sv Server
	var have string
	err := tx.QueryRowContext(ctx, `
        SELECT server_secret_hash, id, owner_user_id, name, slug, plan, version, last_seen_at, direct_ip::text, direct_port, created_at, extra_streams
          FROM servers WHERE id = $1 AND owner_user_id = $2 AND last_seen_at IS NULL FOR UPDATE
    `, serverID, ownerID).Scan(&have,
		&sv.ID, &sv.OwnerUserID, &sv.Name, &sv.Slug, &sv.Plan, &sv.Version, &sv.LastSeenAt, &sv.DirectIP, &sv.DirectPort, &sv.CreatedAt, &sv.ExtraStreams,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrClaimInvalid
	}
	if err != nil {
		return Server{}, err
	}
	if isSecret == nil || !isSecret(have) {
		if _, err := tx.ExecContext(ctx, `UPDATE servers SET server_secret_hash = $2 WHERE id = $1`, sv.ID, secretHash); err != nil {
			return Server{}, err
		}
	}
	return sv, tx.Commit()
}

// RetryKeyMin is the shortest retry key taken: 32 random bytes are 64 hex digits. A shorter one
// counts as none, as does one longer than 256.
const RetryKeyMin = 64

// ValidRetryKey says whether key counts as one.
func ValidRetryKey(key string) bool { return len(key) >= RetryKeyMin && len(key) <= 256 }

// hashRetryKey is what the claim keeps of an agent's retry key (NULL for none).
func hashRetryKey(key string) sql.NullString {
	if !ValidRetryKey(key) {
		return sql.NullString{}
	}
	sum := sha256.Sum256([]byte("maktaba-claim-retry:" + key))
	return sql.NullString{String: hex.EncodeToString(sum[:]), Valid: true}
}

// HasSlug reports whether a server has the slug: the relay edge's TLS
// gate (internal/tls HostPolicy) issues certificates for those only.
func (s *Servers) HasSlug(ctx context.Context, slug string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM servers WHERE slug = $1`, slug).Scan(&n)
	return n > 0, err
}

// ListByOwner returns the user's servers.
func (s *Servers) ListByOwner(ctx context.Context, userID string) ([]Server, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, owner_user_id, name, slug, plan, version, last_seen_at, direct_ip::text, direct_port, created_at, extra_streams
        FROM servers WHERE owner_user_id = $1 ORDER BY created_at DESC
    `, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		var sv Server
		if err := rows.Scan(&sv.ID, &sv.OwnerUserID, &sv.Name, &sv.Slug, &sv.Plan, &sv.Version, &sv.LastSeenAt, &sv.DirectIP, &sv.DirectPort, &sv.CreatedAt, &sv.ExtraStreams); err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

// ByID looks a server up by its UUID. Used by the WS auth flow where
// the agent presents `server_id`+`secret`.
func (s *Servers) ByID(ctx context.Context, id string) (Server, error) {
	var sv Server
	err := s.DB.QueryRowContext(ctx, `
        SELECT id, owner_user_id, name, slug, plan, version, last_seen_at, direct_ip::text, direct_port, created_at, extra_streams
        FROM servers WHERE id = $1
    `, id).Scan(&sv.ID, &sv.OwnerUserID, &sv.Name, &sv.Slug, &sv.Plan, &sv.Version, &sv.LastSeenAt, &sv.DirectIP, &sv.DirectPort, &sv.CreatedAt, &sv.ExtraStreams)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrServerNotFound
	}
	return sv, err
}

// BySlugOrID accepts either a UUID or a slug. Used by the public-facing
// handler so the user can deep-link by whichever they remember.
func (s *Servers) BySlugOrID(ctx context.Context, ref string) (Server, error) {
	if looksLikeUUID(ref) {
		return s.ByID(ctx, ref)
	}
	return s.BySlug(ctx, ref)
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// BySlug is what the relay uses to route incoming HTTPS by hostname.
func (s *Servers) BySlug(ctx context.Context, slug string) (Server, error) {
	var sv Server
	err := s.DB.QueryRowContext(ctx, `
        SELECT id, owner_user_id, name, slug, plan, version, last_seen_at, direct_ip::text, direct_port, created_at, extra_streams
        FROM servers WHERE slug = $1
    `, slug).Scan(&sv.ID, &sv.OwnerUserID, &sv.Name, &sv.Slug, &sv.Plan, &sv.Version, &sv.LastSeenAt, &sv.DirectIP, &sv.DirectPort, &sv.CreatedAt, &sv.ExtraStreams)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrServerNotFound
	}
	return sv, err
}

// Heartbeat updates server_health and bumps last_seen_at.
func (s *Servers) Heartbeat(ctx context.Context, serverID string, online bool, relayMS, directMS int, cpu, mem, storage float32) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO server_health (server_id, online, last_heartbeat, relay_latency_ms, direct_latency_ms, cpu_pct, mem_pct, storage_pct, updated_at)
        VALUES ($1,$2,now(),$3,$4,$5,$6,$7,now())
        ON CONFLICT (server_id) DO UPDATE SET
            online = EXCLUDED.online,
            last_heartbeat = now(),
            relay_latency_ms = EXCLUDED.relay_latency_ms,
            direct_latency_ms = EXCLUDED.direct_latency_ms,
            cpu_pct = EXCLUDED.cpu_pct,
            mem_pct = EXCLUDED.mem_pct,
            storage_pct = EXCLUDED.storage_pct,
            updated_at = now()
    `, serverID, online, relayMS, directMS, cpu, mem, storage)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE servers SET last_seen_at = now() WHERE id = $1`, serverID)
	return err
}

// hashClaim is the at-rest representation of an 8-char claim token.
func hashClaim(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(code)))
	return hex.EncodeToString(sum[:])
}
