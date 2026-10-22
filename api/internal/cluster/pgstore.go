package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ErrUnknownStage is returned by SetStages for a stage not in job_stages.
var ErrUnknownStage = errors.New("cluster: unknown stage")

// PGStore is the Postgres Store. The home's own database user runs it
// (the maktaba_worker group has no access to these tables).
type PGStore struct {
	DB *sql.DB
}

var _ Store = (*PGStore)(nil)

const identityCols = `instance_id::text, name, accept_joins, accept_joins_until, created_at, updated_at`

func scanIdentity(row interface{ Scan(...any) error }) (Identity, error) {
	var i Identity
	var until sql.NullTime
	if err := row.Scan(&i.InstanceID, &i.Name, &i.AcceptJoins, &until, &i.CreatedAt, &i.UpdatedAt); err != nil {
		return Identity{}, err
	}
	if until.Valid {
		t := until.Time
		i.AcceptJoinsUntil = &t
	}
	return i, nil
}

// EnsureIdentity implements Store.
func (s *PGStore) EnsureIdentity(ctx context.Context, name string) (Identity, error) {
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO instance_identity (id, instance_id, name) VALUES (1, $1, $2) ON CONFLICT (id) DO NOTHING`,
		uuid.NewString(), name); err != nil {
		return Identity{}, fmt.Errorf("ensure identity: %w", err)
	}
	return s.Identity(ctx)
}

// Identity implements Store.
func (s *PGStore) Identity(ctx context.Context) (Identity, error) {
	return scanIdentity(s.DB.QueryRowContext(ctx, `SELECT `+identityCols+` FROM instance_identity WHERE id = 1`))
}

// SetAcceptJoins implements Store.
func (s *PGStore) SetAcceptJoins(ctx context.Context, on bool, until *time.Time) (Identity, error) {
	return scanIdentity(s.DB.QueryRowContext(ctx,
		`UPDATE instance_identity SET accept_joins = $1, accept_joins_until = $2, updated_at = now()
		  WHERE id = 1 RETURNING `+identityCols, on, nullTime(until)))
}

// CloseExpiredJoins implements Store.
func (s *PGStore) CloseExpiredJoins(ctx context.Context, now time.Time) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE instance_identity SET accept_joins = false, accept_joins_until = NULL, updated_at = now()
		  WHERE id = 1 AND accept_joins AND accept_joins_until IS NOT NULL AND accept_joins_until <= $1`, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetName implements Store.
func (s *PGStore) SetName(ctx context.Context, name string) (Identity, error) {
	return scanIdentity(s.DB.QueryRowContext(ctx,
		`UPDATE instance_identity SET name = $1, updated_at = now() WHERE id = 1 RETURNING `+identityCols, name))
}

const nodeCols = `id::text, name, state, os, arch, versions, capabilities, storage, path_map, path_map_source,
	drain, node_pubkey, home_pubkey, home_privkey, code_attempts, expires_at, sealed, db_role, remote_addr,
	api_url, created_at, approved_at, approved_by, revoked_at, last_seen_at, updated_at,
	version, platform, web_url, full_node, vod, update_state, replica, replica_reported_at`

func scanNode(row interface{ Scan(...any) error }) (Node, error) {
	var n Node
	var os, arch, pms, hpub, hpriv, role, raddr, apiURL, by, ver, plat, web sql.NullString
	var versions, caps, storage, pathMap, sealed, vod, upd, copyState []byte
	var expires, approved, revoked, seen, copyAt sql.NullTime
	if err := row.Scan(&n.ID, &n.Name, &n.State, &os, &arch, &versions, &caps, &storage, &pathMap, &pms,
		&n.Drain, &n.NodePubkey, &hpub, &hpriv, &n.CodeAttempts, &expires, &sealed, &role, &raddr,
		&apiURL, &n.CreatedAt, &approved, &by, &revoked, &seen, &n.UpdatedAt,
		&ver, &plat, &web, &n.FullNode, &vod, &upd, &copyState, &copyAt); err != nil {
		return Node{}, err
	}
	n.Version, n.Platform, n.WebURL = strPtr(ver), strPtr(plat), strPtr(web)
	n.VOD, n.UpdateState, n.Copy = rawObj(vod), rawObj(upd), rawObj(copyState)
	if copyAt.Valid {
		t := copyAt.Time
		n.CopyReportedAt = &t
	}
	n.OS, n.Arch, n.PathMapSource = strPtr(os), strPtr(arch), strPtr(pms)
	n.HomePubkey, n.HomePrivkey, n.DBRole = strPtr(hpub), strPtr(hpriv), strPtr(role)
	n.RemoteAddr, n.APIURL, n.ApprovedBy = strPtr(raddr), strPtr(apiURL), strPtr(by)
	n.Versions, n.Capabilities, n.Storage = rawObj(versions), rawObj(caps), rawObj(storage)
	n.PathMap, n.Sealed = rawOrNil(pathMap), rawOrNil(sealed)
	n.ExpiresAt, n.ApprovedAt, n.RevokedAt, n.LastSeenAt = timePtr(expires), timePtr(approved), timePtr(revoked), timePtr(seen)
	return n, nil
}

// Node implements Store.
func (s *PGStore) Node(ctx context.Context, id string) (Node, error) {
	n, err := scanNode(s.DB.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM cluster_nodes WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	return n, err
}

// Nodes implements Store.
func (s *PGStore) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+nodeCols+` FROM cluster_nodes ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// PutPending implements Store. An approved or revoked row is left alone
// (the caller answers 409 first; the WHERE makes it race-safe).
func (s *PGStore) PutPending(ctx context.Context, n Node) error {
	res, err := s.DB.ExecContext(ctx, `
INSERT INTO cluster_nodes (id, name, state, os, arch, versions, capabilities, storage, node_pubkey,
	home_pubkey, home_privkey, code_attempts, expires_at, remote_addr, api_url, created_at, updated_at)
VALUES ($1, $2, 'pending', $3, $4, $5, $6, $7, $8, $9, $10, 0, $11, $12, $13, $14, $14)
ON CONFLICT (id) DO UPDATE SET
	name = EXCLUDED.name, state = 'pending', os = EXCLUDED.os, arch = EXCLUDED.arch,
	versions = EXCLUDED.versions, capabilities = EXCLUDED.capabilities, storage = EXCLUDED.storage,
	node_pubkey = EXCLUDED.node_pubkey, home_pubkey = EXCLUDED.home_pubkey,
	home_privkey = EXCLUDED.home_privkey, code_attempts = 0, expires_at = EXCLUDED.expires_at,
	sealed = NULL, remote_addr = EXCLUDED.remote_addr, api_url = EXCLUDED.api_url,
	created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at
WHERE cluster_nodes.state IN ('pending', 'rejected')`,
		n.ID, n.Name, nullStr(n.OS), nullStr(n.Arch), jsonArg(n.Versions), jsonArg(n.Capabilities),
		jsonArg(n.Storage), n.NodePubkey, nullStr(n.HomePubkey), nullStr(n.HomePrivkey),
		nullTime(n.ExpiresAt), nullStr(n.RemoteAddr), nullStr(n.APIURL), n.CreatedAt)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrAlreadyMember
	}
	return nil
}

// ErrAlreadyMember: the node id is approved or revoked.
var ErrAlreadyMember = errors.New("cluster: node is already a member")

// WrongCode implements Store.
func (s *PGStore) WrongCode(ctx context.Context, id string, maxAttempts int, now time.Time) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `
UPDATE cluster_nodes SET code_attempts = code_attempts + 1,
	state = CASE WHEN code_attempts + 1 >= $2 THEN 'rejected' ELSE state END,
	home_privkey = CASE WHEN code_attempts + 1 >= $2 THEN NULL ELSE home_privkey END,
	updated_at = $3
 WHERE id = $1 AND state = 'pending' RETURNING code_attempts`, id, maxAttempts, now).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return n, err
}

// Approve implements Store.
func (s *PGStore) Approve(ctx context.Context, id, dbRole string, sealed json.RawMessage, by string, now time.Time) error {
	return s.exec1(ctx, `
UPDATE cluster_nodes SET state = 'approved', db_role = $2, sealed = $3, approved_by = $4,
	approved_at = $5, home_privkey = NULL, revoked_at = NULL, updated_at = $5
 WHERE id = $1 AND state = 'pending'`, id, dbRole, jsonArg(sealed), by, now)
}

// Reject implements Store.
func (s *PGStore) Reject(ctx context.Context, id string, now time.Time) error {
	return s.exec1(ctx, `
UPDATE cluster_nodes SET state = 'rejected', home_privkey = NULL, sealed = NULL, updated_at = $2
 WHERE id = $1 AND state = 'pending'`, id, now)
}

// Revoke implements Store.
func (s *PGStore) Revoke(ctx context.Context, id string, now time.Time) error {
	if err := s.exec1(ctx, `
UPDATE cluster_nodes SET state = 'revoked', home_privkey = NULL, sealed = NULL, revoked_at = $2, updated_at = $2
 WHERE id = $1 AND state = 'approved'`, id, now); err != nil {
		return err
	}
	// a revoked machine streams no more TV (Epic 32.1 EC2); its role can no longer log
	// in, so it stops beating and its channels fail over. Best effort: the table is
	// absent before slot 0123.
	_, _ = s.DB.ExecContext(ctx, `UPDATE streaming_hosts SET enabled = false WHERE host_id = $1 AND role = 'node'`, id)
	return nil
}

// UpdateNode implements Store.
func (s *PGStore) UpdateNode(ctx context.Context, id string, p NodePatch, now time.Time) error {
	var pm any
	setMap := p.ClearPathMap || p.PathMap != nil
	if p.PathMap != nil {
		pm = string(p.PathMap)
	}
	return s.exec1(ctx, `
UPDATE cluster_nodes SET
	name = COALESCE($2, name),
	drain = COALESCE($3, drain),
	path_map = CASE WHEN $4 THEN $5::jsonb ELSE path_map END,
	path_map_source = CASE WHEN $4 THEN (CASE WHEN $5::jsonb IS NULL THEN NULL ELSE 'admin' END) ELSE path_map_source END,
	updated_at = $6
 WHERE id = $1`, id, nullStr(p.Name), nullBool(p.Drain), setMap, pm, now)
}

// DeleteNode implements Store.
func (s *PGStore) DeleteNode(ctx context.Context, id string) error {
	if err := s.exec1(ctx, `DELETE FROM cluster_nodes WHERE id = $1`, id); err != nil {
		return err
	}
	// its streaming host row goes too (placements on it cascade; best effort)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM streaming_hosts WHERE host_id = $1 AND role = 'node'`, id)
	return nil
}

// SetStages implements Store.
func (s *PGStore) SetStages(ctx context.Context, id string, stages map[string]*bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for stage, on := range stages {
		if on == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM cluster_node_stages WHERE node_id = $1 AND stage = $2`, id, stage)
		} else {
			_, err = tx.ExecContext(ctx, `
INSERT INTO cluster_node_stages (node_id, stage, enabled) VALUES ($1, $2, $3)
ON CONFLICT (node_id, stage) DO UPDATE SET enabled = EXCLUDED.enabled`, id, stage, *on)
		}
		if err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23503" {
				return fmt.Errorf("%w: %s", ErrUnknownStage, stage)
			}
			return err
		}
	}
	return tx.Commit()
}

// Stages implements Store.
func (s *PGStore) Stages(ctx context.Context) (map[string]map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT node_id::text, stage, enabled FROM cluster_node_stages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var id, stage string
		var on bool
		if err := rows.Scan(&id, &stage, &on); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]bool{}
		}
		out[id][stage] = on
	}
	return out, rows.Err()
}

// Workers implements Store.
func (s *PGStore) Workers(ctx context.Context) ([]NodeWorker, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, node_id::text, host, stages, stages_off, last_seen_at
  FROM workers WHERE node_id IS NOT NULL ORDER BY node_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeWorker{}
	for rows.Next() {
		var w NodeWorker
		var stages pq.StringArray
		var off []byte
		if err := rows.Scan(&w.ID, &w.NodeID, &w.Host, &stages, &off, &w.LastSeenAt); err != nil {
			return nil, err
		}
		w.Stages = append([]string{}, stages...)
		w.StagesOff = rawObj(off)
		out = append(out, w)
	}
	return out, rows.Err()
}

// ClearSealed implements Store.
func (s *PGStore) ClearSealed(ctx context.Context, cutoff time.Time) error {
	_, err := s.DB.ExecContext(ctx, `
UPDATE cluster_nodes n SET sealed = NULL, updated_at = now()
 WHERE n.sealed IS NOT NULL AND n.state = 'approved'
   AND (n.approved_at < $1 OR EXISTS (SELECT 1 FROM workers w WHERE w.node_id = n.id))`, cutoff)
	return err
}

func (s *PGStore) exec1(ctx context.Context, q string, args ...any) error {
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func strPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func rawObj(b []byte) json.RawMessage {
	if len(b) == 0 || !json.Valid(b) {
		return json.RawMessage("{}")
	}
	return append(json.RawMessage(nil), b...)
}

func rawOrNil(b []byte) json.RawMessage {
	if len(b) == 0 || string(b) == "null" || !json.Valid(b) {
		return nil
	}
	return append(json.RawMessage(nil), b...)
}

func nullStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func jsonArg(b json.RawMessage) any {
	if b == nil {
		return "{}"
	}
	return string(b)
}
