// Package switchover moves the home to another machine (specs/epics/37-node-replica,
// "Switching the home"): the database steps on both ends — the old home frozen with
// the switchover's marker as its last write, what replication leaves out gathered into a
// kit (the home-only rows, the sequences, the machines' database roles, the home's key
// files), the machine taking over promoted from its copy with it, and the old home made a
// copy of the new one. Nothing here is a manual step; the supervisor runs it when the
// home's API asks (a planned switch) or the admin of the machine taking over does (an
// emergency, the old home gone).
package switchover

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/lib/pq"
)

// KitTables are the tables replication leaves out that the new home needs as they are:
// the home's secrets and tokens, the machines and their stage switches, the licence, the
// devices, the audit log (cluster_copy_excluded() but each database's own state and the
// caches, which the new home makes again). A table this home lacks (an older schema) is
// skipped.
var KitTables = []string{
	"home_secrets", "tv_cluster_secret", "tv_push_secrets", "tv_simulcast_destinations",
	"refresh_tokens", "password_reset_tokens", "pairing_tickets", "cluster_sso_codes",
	"idempotency_keys", "licenses", "devices", "device_downloads", "audit_log",
	"cluster_nodes", "cluster_node_stages",
}

// Sequence is a sequence's position.
type Sequence struct {
	Name      string `json:"name"`
	LastValue int64  `json:"last_value"`
	IsCalled  bool   `json:"is_called"`
}

// Role is a machine's database role as the old home has it: its SCRAM verifier (the
// machine keeps its password), whether it signs in and replicates, its groups.
type Role struct {
	Name        string   `json:"name"`
	Verifier    string   `json:"verifier"`
	Login       bool     `json:"login"`
	Replication bool     `json:"replication"`
	Groups      []string `json:"groups"`
}

// Kit is what the new home takes from the old one besides its copy. Files are the
// home's key files by name (secrets.key, the cluster key, the Postgres CA...).
type Kit struct {
	Switchover string                       `json:"switchover"`
	Term       int64                        `json:"term"`
	Tables     map[string][]json.RawMessage `json:"-"`
	Sequences  []Sequence                   `json:"sequences"`
	Roles      []Role                       `json:"roles"`
	Files      map[string][]byte            `json:"files"`
}

// machineRole: the roles of the machines and of their copies (cluster.RoleName,
// nodecopy.Copy.Role).
func machineRole(name string) bool {
	return strings.HasPrefix(name, "maktaba_node_") || strings.HasPrefix(name, "maktaba_rep_")
}

// Gather reads the kit from the old home, frozen: super is its superuser (it reads the
// roles' verifiers), files the key files to carry.
func Gather(ctx context.Context, super *sql.DB, id string, term int64, files map[string][]byte) (*Kit, error) {
	k := &Kit{Switchover: id, Term: term, Tables: map[string][]json.RawMessage{}, Files: files}
	for _, t := range KitTables {
		var exists bool
		if err := super.QueryRowContext(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, t).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		rows, err := super.QueryContext(ctx, `SELECT row_to_json(t)::text FROM public.`+pq.QuoteIdentifier(t)+` t`)
		if err != nil {
			return nil, fmt.Errorf("switchover: read %s: %w", t, err)
		}
		list := []json.RawMessage{}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				_ = rows.Close()
				return nil, err
			}
			list = append(list, json.RawMessage(s))
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		k.Tables[t] = list
	}
	seqs, err := sequences(ctx, super)
	if err != nil {
		return nil, err
	}
	for _, s := range seqs {
		var q Sequence
		q.Name = s
		if err := super.QueryRowContext(ctx, `SELECT last_value, is_called FROM `+s).Scan(&q.LastValue, &q.IsCalled); err != nil {
			return nil, fmt.Errorf("switchover: read %s: %w", s, err)
		}
		k.Sequences = append(k.Sequences, q)
	}
	rows, err := super.QueryContext(ctx, `
SELECT r.rolname, COALESCE(r.rolpassword, ''), r.rolcanlogin, r.rolreplication,
       COALESCE(array_agg(g.rolname ORDER BY g.rolname) FILTER (WHERE g.rolname IS NOT NULL), '{}')
  FROM pg_authid r
  LEFT JOIN pg_auth_members m ON m.member = r.oid
  LEFT JOIN pg_roles g ON g.oid = m.roleid
 WHERE r.rolname LIKE 'maktaba\_node\_%' OR r.rolname LIKE 'maktaba\_rep\_%'
 GROUP BY 1, 2, 3, 4 ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("switchover: read the machines' roles: %w", err)
	}
	for rows.Next() {
		var r Role
		var groups pq.StringArray
		if err := rows.Scan(&r.Name, &r.Verifier, &r.Login, &r.Replication, &groups); err != nil {
			_ = rows.Close()
			return nil, err
		}
		r.Groups = groups
		k.Roles = append(k.Roles, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return k, nil
}

// sequences are public's sequences, qualified.
func sequences(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT format('%I.%I', n.nspname, c.relname)
	  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	 WHERE c.relkind = 'S' AND n.nspname = 'public' ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// The kit on the wire: gzip of JSON lines, each one of {"kit": meta}, {"t": table, "r":
// row}, closed by {"end": true}.
type line struct {
	Kit *Kit            `json:"kit,omitempty"`
	T   string          `json:"t,omitempty"`
	R   json.RawMessage `json:"r,omitempty"`
	End bool            `json:"end,omitempty"`
}

// Write writes the kit to w.
func (k *Kit) Write(w io.Writer) error {
	zw := gzip.NewWriter(w)
	enc := json.NewEncoder(zw)
	if err := enc.Encode(line{Kit: k}); err != nil {
		return err
	}
	names := make([]string, 0, len(k.Tables))
	for t := range k.Tables {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, t := range names {
		// an empty table still says it was gathered (the new home empties its own)
		if err := enc.Encode(line{T: t}); err != nil {
			return err
		}
		for _, r := range k.Tables[t] {
			if err := enc.Encode(line{T: t, R: r}); err != nil {
				return err
			}
		}
	}
	if err := enc.Encode(line{End: true}); err != nil {
		return err
	}
	return zw.Close()
}

// ReadKit reads a kit Write wrote.
func ReadKit(r io.Reader) (*Kit, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	var k *Kit
	for sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("switchover: the kit: %w", err)
		}
		switch {
		case l.Kit != nil:
			k = l.Kit
			k.Tables = map[string][]json.RawMessage{}
		case k == nil:
			return nil, errors.New("switchover: the kit has no head")
		case l.End:
			return k, nil
		case l.R == nil:
			if _, ok := k.Tables[l.T]; !ok {
				k.Tables[l.T] = []json.RawMessage{}
			}
		default:
			k.Tables[l.T] = append(k.Tables[l.T], l.R)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, ErrIncomplete
}
