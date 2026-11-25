package dbsafe

// How a restore empties the public schema of a Postgres the home does not bundle.
//
// Afresh: the schema is dropped and made again, with its extensions. That takes a database
// user who owns the schema and may make each of them: a superuser, where pgvector is in public
// (it is not a trusted extension).
//
// Inside: for one who may not, the schema and its extensions stay, and what is in public, but
// for the extensions' own, is dropped in the restore's transaction: the user's tables, views,
// sequences, functions, types and the like, and the default privileges there. The dump's items
// that make the schema and the extensions are left out. One transaction, so only with psql: an
// error commits nothing. What cannot work is refused before anything is touched.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// publicAfresh makes the public schema again, with the use of it every role has in a new
// database (a dump holds a grant on it only where it is another: no role is left out of a
// schema it could use).
const publicAfresh = "DROP SCHEMA public CASCADE;\nCREATE SCHEMA public;\nGRANT USAGE ON SCHEMA public TO PUBLIC;\n"

// withPublic are the extensions that go with the public schema: those in it, and those in other
// schemas that depend on one that goes.
const withPublic = `WITH RECURSIVE gone(oid) AS (
	SELECT e.oid FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE n.nspname = 'public'
	UNION
	SELECT d.objid FROM pg_depend d JOIN gone g ON d.refobjid = g.oid
	WHERE d.classid = 'pg_extension'::regclass AND d.refclassid = 'pg_extension'::regclass)
`

// inPublic is what is in public and no extension's own: what DROP SCHEMA public CASCADE would
// drop first (what depends on the schema itself), with who owns it. What is another object's
// own part (an identity column's sequence, a range's multirange) goes with it (a partitioned
// table is a part of its own key's columns: that is none).
const inPublic = `SELECT i.type, i.identity, coalesce((SELECT s.refobjid FROM pg_shdepend s
		WHERE s.dbid = (SELECT oid FROM pg_database WHERE datname = current_database()) AND s.classid = d.classid
		  AND s.objid = d.objid AND s.deptype = 'o' AND s.refclassid = 'pg_authid'::regclass LIMIT 1), 10) AS owner
	FROM pg_depend d CROSS JOIN LATERAL pg_identify_object(d.classid, d.objid, 0) i
	WHERE d.refclassid = 'pg_namespace'::regclass AND d.refobjid = 'public'::regnamespace AND d.deptype = 'n'
	  AND d.classid <> 'pg_extension'::regclass
	  AND NOT EXISTS (SELECT FROM pg_depend x WHERE x.classid = d.classid AND x.objid = d.objid AND x.deptype IN ('e', 'i')
	      AND NOT (x.refclassid = x.classid AND x.refobjid = x.objid))`

// publicDefaults are the default privileges in public: whose, on what, and whom they are for.
const publicDefaults = `SELECT DISTINCT a.defaclrole AS owner, a.defaclobjtype AS kind, x.grantee
	FROM pg_default_acl a CROSS JOIN LATERAL aclexplode(a.defaclacl) x WHERE a.defaclnamespace = 'public'::regnamespace`

// dropKinds are the kinds of object (pg_identify_object's) a restore inside public drops, and
// what DROP calls them.
var dropKinds = map[string]string{"table": "TABLE", "foreign table": "FOREIGN TABLE", "view": "VIEW", "materialized view": "MATERIALIZED VIEW",
	"sequence": "SEQUENCE", "type": "TYPE", "composite type": "TYPE", "function": "FUNCTION", "procedure": "PROCEDURE",
	"aggregate": "AGGREGATE", "operator": "OPERATOR", "operator class": "OPERATOR CLASS", "operator family": "OPERATOR FAMILY",
	"collation": "COLLATION", "conversion": "CONVERSION", "statistics object": "STATISTICS",
	"text search configuration": "TEXT SEARCH CONFIGURATION", "text search dictionary": "TEXT SEARCH DICTIONARY"}

// emptyPublic is the script that empties public and leaves it and its extensions.
func emptyPublic() string {
	var kinds strings.Builder
	for _, k := range slices.Sorted(maps.Keys(dropKinds)) {
		fmt.Fprintf(&kinds, " WHEN '%s' THEN '%s'", k, dropKinds[k])
	}
	return `DO $maktaba$
DECLARE
	stmts text[];
	s text;
	n bigint := (SELECT count(*) FROM pg_catalog.pg_extension);
BEGIN
	-- all of them first: what one's CASCADE takes along is not there for the next (IF EXISTS)
	SELECT pg_catalog.array_agg(pg_catalog.format('DROP %s IF EXISTS %s CASCADE', CASE o.type` + kinds.String() + ` END, o.identity)) INTO stmts
	FROM (` + inPublic + `) o;
	FOREACH s IN ARRAY coalesce(stmts, '{}') LOOP
		EXECUTE s;
	END LOOP;
	SELECT pg_catalog.array_agg(pg_catalog.format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public REVOKE ALL ON %s FROM %s',
		pg_catalog.pg_get_userbyid(d.owner), CASE d.kind WHEN 'r' THEN 'TABLES' WHEN 'S' THEN 'SEQUENCES' WHEN 'f' THEN 'FUNCTIONS' WHEN 'T' THEN 'TYPES' END,
		CASE d.grantee WHEN 0 THEN 'PUBLIC' ELSE pg_catalog.quote_ident(pg_catalog.pg_get_userbyid(d.grantee)) END)) INTO stmts
	FROM (` + publicDefaults + `) d;
	FOREACH s IN ARRAY coalesce(stmts, '{}') LOOP
		EXECUTE s;
	END LOOP;
	IF (SELECT count(*) FROM pg_catalog.pg_extension) <> n THEN
		RAISE EXCEPTION 'an extension depends on what is not one''s in the public schema';
	END IF;
	IF EXISTS (` + inPublic + `) THEN
		RAISE EXCEPTION 'the public schema could not be emptied';
	END IF;
END
$maktaba$;
`
}

// howToRestore is how a restore into this database is done.
type howToRestore struct {
	// inside: public stays, emptied (the package comment above)
	inside bool
	// empty is the script that empties public
	empty string
	// list is pg_restore's list of what it restores ("": all the dump holds)
	list string
}

// cannotRestore is why this database's user cannot put a dump back: nothing was touched.
type cannotRestore struct{ why string }

func (e *cannotRestore) Error() string { return e.why }

// Cannot is such an error.
func Cannot(why string) error { return &cannotRestore{why} }

// CannotRestore reports whether err says that the database's user cannot put the dump back.
func CannotRestore(err error) bool {
	var c *cannotRestore
	return errors.As(err, &c)
}

// CheckRestore says whether a dump can be put back into the database as it is, and whether
// inside public (a release from before that cannot then). Any other error: it could not be told.
func (t Target) CheckRestore(ctx context.Context, dump string) (inside bool, err error) {
	list, err := t.list(ctx, dump)
	if err != nil {
		return false, err
	}
	d, err := t.open(ctx, "")
	if err != nil {
		return false, err
	}
	defer d.Close()
	h, err := t.howTo(ctx, d, dump, list, havePSQL(t.bin("psql")))
	return h.inside, err
}

// extension is one of the database's, or of its server's.
type extension struct {
	there, goes                    bool // in the database; with public
	available, needsSuper, trusted bool // as its control file says
}

// howTo decides it; a *cannotRestore when no way would work.
func (t Target) howTo(ctx context.Context, d *sql.DB, dump, list string, psql bool) (howToRestore, error) {
	var h howToRestore
	var user string
	var super, create, owns, makes bool
	if err := d.QueryRowContext(ctx, `SELECT current_user, r.rolsuper, has_database_privilege(current_database(), 'CREATE'),
			pg_has_role(current_user, n.nspowner, 'USAGE'), has_schema_privilege(n.oid, 'CREATE')
		FROM pg_roles r, pg_namespace n WHERE r.rolname = current_user AND n.nspname = 'public'`).
		Scan(&user, &super, &create, &owns, &makes); errors.Is(err, sql.ErrNoRows) {
		return h, &cannotRestore{"the database has no public schema"}
	} else if err != nil {
		return h, err
	}
	cannot := func(format string, a ...any) (howToRestore, error) {
		return howToRestore{}, &cannotRestore{fmt.Sprintf(format, a...)}
	}
	if !psql && len(pgbundle.Chunks(dump)) > 0 {
		return cannot("its vectors are binary chunks that psql loads, and there is no psql beside pg_restore (install the " +
			"PostgreSQL client programs, or set MAKTABA_PG_BIN)")
	}
	exts, err := extensions(ctx, d)
	if err != nil {
		return h, err
	}
	schemas, err := column(ctx, d, `SELECT nspname FROM pg_namespace WHERE nspname NOT IN ('public', 'information_schema') AND nspname NOT LIKE 'pg\_%'`)
	if err != nil {
		return h, err
	}
	holds := func(ext string) bool { return strings.Contains(list, " EXTENSION - "+ext+" ") }
	// why public cannot be made again
	again := ""
	if !owns || !create {
		again = "the public schema is not the database user " + user + "'s to make again"
	}
	for name, e := range exts {
		if again == "" && e.goes && holds(name) && !mayMake(super, create, e.available, e.needsSuper, e.trusted) {
			again = fmt.Sprintf("the extension %s in the public schema is not one the database user %s may make again (it needs a "+
				"superuser, or is no longer installed)", name, user)
		}
	}
	skip := map[string]bool{}
	skipSchema := func(s string, grants bool) {
		skip["SCHEMA - "+s], skip["COMMENT - SCHEMA "+s] = true, true
		skip["ACL - SCHEMA "+s] = grants
	}
	for _, s := range schemas {
		skipSchema(s, true)
	}
	if again == "" {
		h.empty = publicAfresh
		for name, e := range exts {
			if e.there && !e.goes {
				skip["EXTENSION - "+name], skip["COMMENT - EXTENSION "+name] = true, true
			}
		}
		h.list = withoutItems(list, skip)
		return h, nil
	}

	// inside
	if !psql {
		return cannot("%s; so the restore empties it instead, in one transaction, which takes psql, and there is none beside "+
			"pg_restore (install the PostgreSQL client programs, or set MAKTABA_PG_BIN)", again)
	}
	again += "; so the restore empties it instead, and "
	if !makes {
		return cannot(again+"%s may make nothing in it (GRANT CREATE ON SCHEMA public TO %s)", user, user)
	}
	foreign, err := t.notMine(ctx, d)
	if err != nil {
		return h, err
	}
	if len(foreign) > 0 {
		return cannot(again+"%s may not drop what is another's there: %s", user, few(foreign))
	}
	skipSchema("public", !owns)
	for name, e := range exts {
		if e.there {
			skip["EXTENSION - "+name], skip["COMMENT - EXTENSION "+name] = true, true
		} else if holds(name) && !mayMake(super, create, e.available, e.needsSuper, e.trusted) {
			return cannot(again+"the backup needs the extension %s, which the database lacks and %s may not make (CREATE EXTENSION "+
				"%s, as a superuser)", name, user, name)
		}
	}
	// a backup of an extension this server does not have
	for _, e := range pgbundle.ParseTOC(list) {
		if _, known := exts[e.Name]; e.Desc == "EXTENSION" && !known {
			return cannot(again+"the backup needs the extension %s, which this server does not have", e.Name)
		}
	}
	members, err := column(ctx, d, `SELECT c.relname FROM pg_class c JOIN pg_depend x ON x.classid = 'pg_class'::regclass AND x.objid = c.oid AND x.deptype = 'e'
		WHERE c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p')`)
	if err != nil {
		return h, err
	}
	for _, e := range pgbundle.ParseTOC(list) {
		if e.Desc == "TABLE DATA" && e.Schema == "public" && slices.Contains(members, e.Name) {
			return cannot(again+"the backup holds rows of %s, a table of an extension that stays", e.Name)
		}
	}
	h.inside, h.empty, h.list = true, emptyPublic(), withoutItems(list, skip)
	return h, nil
}

// notMine names what is in public that the database user may not drop, or whose default
// privileges it may not change.
func (t Target) notMine(ctx context.Context, d *sql.DB) ([]string, error) {
	kinds := slices.Sorted(maps.Keys(dropKinds))
	out, err := column(ctx, d, `SELECT o.type || ' ' || o.identity || ' (' || pg_get_userbyid(o.owner) || ')' FROM (`+inPublic+`) o
		WHERE NOT pg_has_role(current_user, o.owner, 'USAGE') OR o.type NOT IN ('`+strings.Join(kinds, "', '")+`') ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defaults, err := column(ctx, d, `SELECT DISTINCT 'the default privileges of ' || pg_get_userbyid(o.owner) FROM (`+publicDefaults+`) o
		WHERE NOT pg_has_role(current_user, o.owner, 'USAGE') ORDER BY 1`)
	return append(out, defaults...), err
}

// extensions are the database's extensions and its server's.
func extensions(ctx context.Context, d *sql.DB) (map[string]extension, error) {
	rows, err := d.QueryContext(ctx, withPublic+`SELECT coalesce(e.extname, a.name), e.oid IS NOT NULL, coalesce(e.oid IN (SELECT oid FROM gone), false),
			v.name IS NOT NULL, coalesce(v.superuser, true), coalesce(v.trusted, false)
		FROM pg_available_extensions a FULL JOIN pg_extension e ON e.extname = a.name
		LEFT JOIN pg_available_extension_versions v ON v.name = a.name AND v.version = a.default_version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]extension{}
	for rows.Next() {
		var name string
		var e extension
		if err := rows.Scan(&name, &e.there, &e.goes, &e.available, &e.needsSuper, &e.trusted); err != nil {
			return nil, err
		}
		out[name] = e
	}
	return out, rows.Err()
}

// mayMake reports whether a database user (super: a superuser, create: with CREATE on the
// database) may make an extension, as its control file says (a hosted Postgres may let a user
// make one it says needs a superuser: the restore then goes on inside public all the same).
func mayMake(super, create, available, needsSuper, trusted bool) bool {
	return available && (super || !needsSuper || (trusted && create))
}

// mayNotMakeIn names the first of d's database's extensions that the database user may not make in
// the scratch database ("": all of them). It is tried, and undone: a hosted Postgres may let a
// user make one its control file says needs a superuser.
func (t Target) mayNotMakeIn(ctx context.Context, d *sql.DB, scratch string) (string, error) {
	rows, err := d.QueryContext(ctx, `SELECT e.extname, n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
		WHERE e.extname <> 'plpgsql' ORDER BY 1`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	s, err := t.open(ctx, scratch)
	if err != nil {
		return "", err
	}
	defer s.Close()
	for rows.Next() {
		var ext, schema string
		if err := rows.Scan(&ext, &schema); err != nil {
			return "", err
		}
		tx, err := s.BeginTx(ctx, nil)
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+pq.QuoteIdentifier(schema))
		if err == nil {
			_, err = tx.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS `+pq.QuoteIdentifier(ext)+` SCHEMA `+pq.QuoteIdentifier(schema)+` CASCADE`)
		}
		_ = tx.Rollback()
		var pe *pq.Error
		if errors.As(err, &pe) && pe.Code == "42501" { // insufficient_privilege; any other the restore says
			return ext, nil
		}
	}
	return "", rows.Err()
}

// column is a query's one column.
func column(ctx context.Context, d *sql.DB, q string) ([]string, error) {
	rows, err := d.QueryContext(ctx, q)
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

// few names the first three.
func few(names []string) string {
	if len(names) > 3 {
		return fmt.Sprintf("%s, and %d more", strings.Join(names[:3], ", "), len(names)-3)
	}
	return strings.Join(names, ", ")
}

// listFile writes pg_restore's list to a file ("" when it is the dump's own, whole).
func listFile(list, whole string) (string, error) {
	if list == whole {
		return "", nil
	}
	f, err := os.CreateTemp("", "maktaba-restore-list-*")
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(list)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// useList is pg_restore's argument for listFile's.
func useList(file string) []string {
	if file == "" {
		return nil
	}
	return []string{"--use-list=" + file}
}
