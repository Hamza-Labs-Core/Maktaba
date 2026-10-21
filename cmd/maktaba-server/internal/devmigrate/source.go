package devmigrate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// Source is the stack's database in its Docker container.
type Source struct {
	sys       Sys
	Container string
	User      string
	Database  string
}

// NewSource is the database user/name of the stack's URL in container.
func NewSource(sys Sys, container string, s Settings) (Source, error) {
	user, db, err := s.DB()
	if err != nil {
		return Source{}, err
	}
	return Source{sys: sys, Container: container, User: user, Database: db}, nil
}

// Container is what Docker says of the container.
type Container struct {
	Exists  bool
	Running bool
	Image   string
	Volume  string // the named volume holding the data ("" a bind mount or none)
}

// Inspect asks Docker about the container.
func (s Source) Inspect(ctx context.Context) Container {
	out := s.sys.output(ctx, "docker", "inspect", s.Container)
	var info []struct {
		State  struct{ Running bool }
		Config struct{ Image string }
		Mounts []struct {
			Type, Name, Destination string
		}
	}
	if json.Unmarshal(out, &info) != nil || len(info) == 0 {
		return Container{}
	}
	c := Container{Exists: true, Running: info[0].State.Running, Image: info[0].Config.Image}
	for _, m := range info[0].Mounts {
		if m.Type == "volume" && strings.HasPrefix(m.Destination, "/var/lib/postgresql") {
			c.Volume = m.Name
		}
	}
	return c
}

func (s Source) psql(interactive bool) []string {
	args := []string{"exec"}
	if interactive {
		args = append(args, "-i")
	}
	return append(args, s.Container, "psql", "-X", "-q", "-At", "-v", "ON_ERROR_STOP=1", "-U", s.User, "-d", s.Database)
}

// Query runs one statement in the container and returns its first row.
func (s Source) Query(ctx context.Context, sql string) (string, error) {
	cmd := s.sys.Command(ctx, "docker", append(s.psql(false), "-c", sql)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("psql in %s: %w: %s", s.Container, err, strings.TrimSpace(stderr.String()))
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return line, nil
}

// Snapshot is a read-only transaction held open in the container: its counts
// and the dump made with its snapshot see the very same rows (machines that
// joined this home may still write while it runs).
type Snapshot struct {
	ID   string
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	errs *bytes.Buffer
}

// Snapshot opens the transaction and exports its snapshot.
func (s Source) Snapshot(ctx context.Context) (*Snapshot, error) {
	cmd := s.sys.Command(ctx, "docker", s.psql(true)...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	sn := &Snapshot{cmd: cmd, in: in, out: bufio.NewReader(out), errs: &bytes.Buffer{}}
	cmd.Stderr = sn.errs
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	id, err := sn.Query("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SELECT pg_export_snapshot();")
	if err != nil {
		_ = sn.Close()
		return nil, err
	}
	sn.ID = id
	return sn, nil
}

// Query sends one statement and reads its one-line answer.
func (sn *Snapshot) Query(sql string) (string, error) {
	if !strings.HasSuffix(strings.TrimSpace(sql), ";") {
		sql += ";" // psql waits for the end of the statement
	}
	if _, err := io.WriteString(sn.in, sql+"\n"); err != nil {
		return "", sn.fail(err)
	}
	l, err := sn.out.ReadString('\n')
	if err != nil {
		return "", sn.fail(err)
	}
	return strings.TrimRight(l, "\n"), nil
}

func (sn *Snapshot) fail(err error) error {
	if msg := strings.TrimSpace(sn.errs.String()); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

// Close ends the transaction.
func (sn *Snapshot) Close() error {
	_, _ = io.WriteString(sn.in, "COMMIT;\n")
	_ = sn.in.Close()
	_, _ = io.Copy(io.Discard, sn.out)
	return sn.cmd.Wait()
}

// Dump writes a custom-format dump of the database at the snapshot to path
// (beside it, then renamed into place).
func (s Source) Dump(ctx context.Context, snapshot, path string) error {
	part := path + ".partial"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	args := []string{"exec", s.Container, "pg_dump", "-Fc", "-U", s.User}
	if snapshot != "" {
		args = append(args, "--snapshot="+snapshot)
	}
	cmd := s.sys.Command(ctx, "docker", append(args, s.Database)...)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = f, &stderr
	err = cmd.Run()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("pg_dump in %s: %w: %s", s.Container, err, strings.TrimSpace(stderr.String()))
	}
	return os.Rename(part, path)
}

// CheckTables are the counts the move must carry exactly: the library, its
// transcripts, the accounts and the Live TV hosts. goose is the migrations'
// version.
var CheckTables = []string{"libraries", "videos", "transcripts", "transcript_segments", "users", "streaming_hosts"}

// Facts are what the source holds, at one snapshot.
type Facts struct {
	Counts     map[string]int64 `json:"counts"`
	Roles      []string         `json:"roles,omitempty"`
	Members    [][2]string      `json:"members,omitempty"`
	Extensions []string         `json:"extensions,omitempty"`
	HostID     string           `json:"host_id,omitempty"` // this home's Live TV host (instance_identity)
	Size       int64            `json:"size"`              // pg_database_size
	Nodes      []string         `json:"nodes,omitempty"`   // the machines' Live TV hosts
}

// querier is one statement in, its first row out: a snapshot session, the
// container, or the bundled Postgres.
type querier func(sql string) (string, error)

// ReadFacts reads the counts and the roles.
func ReadFacts(q querier) (Facts, error) {
	f := Facts{Counts: map[string]int64{}}
	have, err := q(`SELECT coalesce(string_agg(relname, ','), '') FROM pg_class WHERE relkind IN ('r', 'p') ` +
		`AND relnamespace = 'public'::regnamespace AND relname IN ('` + strings.Join(append(CheckTables, "goose_db_version", "instance_identity"), "','") + `')`)
	if err != nil {
		return f, err
	}
	tables := map[string]bool{}
	for _, t := range strings.Split(have, ",") {
		tables[t] = t != ""
	}
	var parts []string
	for _, t := range CheckTables {
		if tables[t] {
			parts = append(parts, "'"+t+"='||(SELECT count(*) FROM "+t+")")
		}
	}
	if tables["goose_db_version"] {
		parts = append(parts, "'goose='||(SELECT coalesce(max(version_id), 0) FROM goose_db_version)")
	}
	parts = append(parts, "'size='||pg_database_size(current_database())")
	row, err := q("SELECT concat_ws(' ', " + strings.Join(parts, ", ") + ")")
	if err != nil {
		return f, err
	}
	for _, kv := range strings.Fields(row) {
		k, v, _ := strings.Cut(kv, "=")
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return f, fmt.Errorf("count %s: %q", k, v)
		}
		if k == "size" {
			f.Size = n
		} else {
			f.Counts[k] = n
		}
	}
	if tables["instance_identity"] {
		if f.HostID, err = q("SELECT coalesce((SELECT instance_id::text FROM instance_identity WHERE id = 1), '')"); err != nil {
			return f, err
		}
	}
	if tables["streaming_hosts"] {
		nodes, err := q("SELECT coalesce(string_agg(host, ',' ORDER BY host), '') FROM streaming_hosts WHERE role = 'node'")
		if err != nil {
			return f, err
		}
		f.Nodes = splitList(nodes)
	}
	roles, err := q(`SELECT coalesce(string_agg(rolname, ',' ORDER BY rolname), '') FROM pg_roles ` +
		`WHERE NOT rolsuper AND rolname !~ '^pg_' AND rolname <> current_user`)
	if err != nil {
		return f, err
	}
	f.Roles = splitList(roles)
	members, err := q(`SELECT coalesce(string_agg(m.rolname || '>' || g.rolname, ','), '') FROM pg_auth_members a ` +
		`JOIN pg_roles m ON m.oid = a.member JOIN pg_roles g ON g.oid = a.roleid ` +
		`WHERE m.rolname !~ '^pg_' AND g.rolname !~ '^pg_' AND NOT g.rolsuper`)
	if err != nil {
		return f, err
	}
	for _, mg := range splitList(members) {
		if m, g, ok := strings.Cut(mg, ">"); ok {
			f.Members = append(f.Members, [2]string{m, g})
		}
	}
	ext, err := q("SELECT coalesce(string_agg(extname, ',' ORDER BY extname), '') FROM pg_extension")
	if err != nil {
		return f, err
	}
	f.Extensions = splitList(ext)
	return f, nil
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Unrestorable names what the bundled Postgres cannot take as the app role:
// extensions it does not make, role names it cannot create.
func (f Facts) Unrestorable() []string {
	var out []string
	for _, e := range f.Extensions {
		if !slices.Contains(pgbundle.Extensions, e) {
			out = append(out, "the extension "+e)
		}
	}
	for _, r := range f.Roles {
		if !pgbundle.ValidRoleName(r) {
			out = append(out, "the role "+r)
		}
	}
	return out
}

// Fence keeps every other writer out before the snapshot: the other login
// roles (the machines that joined this home) become NOLOGIN and every other
// session on the database ends, so nothing is written after the dump that the
// move would lose. It returns the roles it disabled (Undo logs them in again).
func (s Source) Fence(ctx context.Context) ([]string, error) {
	list, err := s.Query(ctx, "SELECT coalesce(string_agg(rolname, ',' ORDER BY rolname), '') FROM pg_roles "+
		"WHERE rolcanlogin AND NOT rolsuper AND rolname <> current_user AND rolname !~ '^pg_'")
	if err != nil {
		return nil, err
	}
	var off []string
	for _, r := range splitList(list) {
		if !pgbundle.ValidRoleName(r) {
			return off, fmt.Errorf("cannot disable the role %q", r)
		}
		if _, err := s.Query(ctx, "ALTER ROLE "+r+" NOLOGIN"); err != nil {
			return off, err
		}
		off = append(off, r)
	}
	_, err = s.Query(ctx, "SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity "+
		"WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend'")
	return off, err
}

// MachineRoles are the roles of machines that joined this home.
func (f Facts) MachineRoles() []string {
	var out []string
	for _, r := range f.Roles {
		if strings.HasPrefix(r, "maktaba_node_") {
			out = append(out, r)
		}
	}
	return out
}

// Compare checks got against want: every count equal (exact) or at least want
// (grown: the app has run since).
func Compare(want, got map[string]int64, exact bool) error {
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var bad []string
	for _, k := range keys {
		g, ok := got[k]
		switch {
		case !ok:
			bad = append(bad, k+": missing")
		case exact && g != want[k], !exact && g < want[k]:
			bad = append(bad, fmt.Sprintf("%s: %d, expected %d", k, g, want[k]))
		}
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "; "))
	}
	return nil
}
