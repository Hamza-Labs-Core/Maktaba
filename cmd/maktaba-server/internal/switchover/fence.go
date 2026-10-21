package switchover

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Fence stops an old home that learned of a newer term from writing: it may have been
// cut off rather than gone, and two homes must never both write. Its database becomes
// read-only for every session, and the sessions open now end.
func Fence(ctx context.Context, super *sql.DB) error {
	if _, err := super.ExecContext(ctx, `ALTER DATABASE `+database+` SET default_transaction_read_only = on`); err != nil {
		return fmt.Errorf("switchover: fence: %w", err)
	}
	_, err := super.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
	    WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend'`)
	return err
}

// Change is one row an old home wrote after the new home's cut.
type Change struct {
	Table string `json:"table"`
	Kind  string `json:"kind"` // insert | update | delete | truncate
	// Row is the row's columns as text (insert, update: the new row; delete: its key);
	// a nil value is NULL, a column the change did not carry is left out.
	Row map[string]*string `json:"row,omitempty"`
}

// Transaction is one transaction of those changes.
type Transaction struct {
	CommitLSN   string    `json:"commit_lsn"`
	CommittedAt time.Time `json:"committed_at"`
	Changes     []Change  `json:"changes"`
}

// Aside is what an old home wrote after the new home's cut: kept on it, and reported.
type Aside struct {
	Slot         string         `json:"slot"`
	Cut          string         `json:"cut_lsn"`
	Transactions []Transaction  `json:"transactions"`
	Counts       map[string]int `json:"counts"` // changes by table
}

// KeptAside reads what the old home wrote after cut (the new home's position in its
// WAL): from the slot of the machine that took over, which still holds every change
// that machine had not confirmed. The slot is only peeked (nothing is consumed: what is
// read stays there until the slot goes). pub is that slot's publication.
func KeptAside(ctx context.Context, super *sql.DB, slot, pub, cut string) (Aside, error) {
	out := Aside{Slot: slot, Cut: cut, Counts: map[string]int{}}
	var cutN uint64
	if cut != "" {
		n, err := parseLSN(cut)
		if err != nil {
			return out, err
		}
		cutN = n
	}
	rows, err := super.QueryContext(ctx, `SELECT data FROM pg_logical_slot_peek_binary_changes($1, NULL, NULL,
	    'proto_version', '1', 'publication_names', $2)`, slot, pub)
	if err != nil {
		return out, fmt.Errorf("switchover: read the slot %s: %w", slot, err)
	}
	defer rows.Close()
	d := decoder{rels: map[uint32]relation{}}
	var tx *Transaction
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return out, err
		}
		m, err := d.message(data)
		if err != nil {
			return out, err
		}
		switch m.kind {
		case 'B':
			tx = &Transaction{}
		case 'C':
			if tx != nil && m.end > cutN {
				tx.CommitLSN = formatLSN(m.commit)
				tx.CommittedAt = pgTime(m.ts)
				if len(tx.Changes) > 0 {
					out.Transactions = append(out.Transactions, *tx)
					for _, c := range tx.Changes {
						out.Counts[c.Table]++
					}
				}
			}
			tx = nil
		case 'I', 'U', 'D', 'T':
			if tx != nil {
				tx.Changes = append(tx.Changes, m.changes...)
			}
		}
	}
	return out, rows.Err()
}

type relation struct {
	name string
	cols []string
}

type decoder struct{ rels map[uint32]relation }

type message struct {
	kind            byte
	commit, end, ts uint64
	changes         []Change
}

// errShort: a pgoutput message ended early.
var errShort = errors.New("switchover: a short pgoutput message")

type reader struct {
	b   []byte
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || len(r.b) < n {
		r.err = errShort
		return make([]byte, n)
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *reader) u8() byte    { return r.take(1)[0] }
func (r *reader) u16() uint16 { return binary.BigEndian.Uint16(r.take(2)) }
func (r *reader) u32() uint32 { return binary.BigEndian.Uint32(r.take(4)) }
func (r *reader) u64() uint64 { return binary.BigEndian.Uint64(r.take(8)) }

func (r *reader) str() string {
	for i, c := range r.b {
		if c == 0 {
			s := string(r.b[:i])
			r.b = r.b[i+1:]
			return s
		}
	}
	r.err = errShort
	return ""
}

// message decodes one pgoutput (protocol 1) message: the ones a report needs.
func (d *decoder) message(b []byte) (message, error) {
	if len(b) == 0 {
		return message{}, errShort
	}
	r := &reader{b: b[1:]}
	m := message{kind: b[0]}
	switch m.kind {
	case 'B':
		r.u64() // final lsn
		m.ts = r.u64()
		r.u32() // xid
	case 'C':
		r.u8() // flags
		m.commit = r.u64()
		m.end = r.u64()
		m.ts = r.u64()
	case 'R':
		id := r.u32()
		ns := r.str()
		name := r.str()
		r.u8() // replica identity
		n := int(r.u16())
		rel := relation{name: ns + "." + name}
		for i := 0; i < n && r.err == nil; i++ {
			r.u8() // flags
			rel.cols = append(rel.cols, r.str())
			r.u32() // type
			r.u32() // typmod
		}
		d.rels[id] = rel
	case 'I':
		rel := d.rels[r.u32()]
		r.u8() // 'N'
		m.changes = []Change{{Table: rel.name, Kind: "insert", Row: tuple(r, rel)}}
	case 'U':
		rel := d.rels[r.u32()]
		k := r.u8()
		if k == 'K' || k == 'O' {
			tuple(r, rel) // the old row
			r.u8()        // 'N'
		}
		m.changes = []Change{{Table: rel.name, Kind: "update", Row: tuple(r, rel)}}
	case 'D':
		rel := d.rels[r.u32()]
		r.u8() // 'K' or 'O'
		m.changes = []Change{{Table: rel.name, Kind: "delete", Row: tuple(r, rel)}}
	case 'T':
		n := int(r.u32())
		r.u8() // options
		for i := 0; i < n && r.err == nil; i++ {
			m.changes = append(m.changes, Change{Table: d.rels[r.u32()].name, Kind: "truncate"})
		}
	}
	if r.err != nil {
		return message{}, r.err
	}
	return m, nil
}

func tuple(r *reader, rel relation) map[string]*string {
	n := int(r.u16())
	row := map[string]*string{}
	for i := 0; i < n && r.err == nil; i++ {
		name := fmt.Sprintf("column_%d", i+1)
		if i < len(rel.cols) {
			name = rel.cols[i]
		}
		switch r.u8() {
		case 'n':
			row[name] = nil
		case 'u': // an unchanged TOASTed value: not carried
		case 't', 'b':
			v := string(r.take(int(r.u32())))
			row[name] = &v
		}
	}
	return row
}

func parseLSN(s string) (uint64, error) {
	var hi, lo uint32
	if _, err := fmt.Sscanf(s, "%X/%X", &hi, &lo); err != nil {
		return 0, fmt.Errorf("switchover: an LSN %q: %w", s, err)
	}
	return uint64(hi)<<32 | uint64(lo), nil
}

func formatLSN(n uint64) string { return fmt.Sprintf("%X/%X", uint32(n>>32), uint32(n)) }

// pgTime is a Postgres timestamp (microseconds since 2000-01-01 UTC).
func pgTime(us uint64) time.Time {
	return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(int64(us)) * time.Microsecond)
}

// Tables are the tables an Aside touches, sorted.
func (a Aside) Tables() []string {
	out := make([]string, 0, len(a.Counts))
	for t := range a.Counts {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
