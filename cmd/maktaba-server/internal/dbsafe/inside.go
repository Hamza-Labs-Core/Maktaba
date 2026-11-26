package dbsafe

// How a restore empties the public schema of a Postgres the home does not bundle.
//
// Afresh: the schema is dropped and made again, with its extensions. That takes a database
// user who owns the schema and may make each of them: a superuser, where pgvector is in public
// (it is not a trusted extension).
//
// Inside: for one who may not, the schema and its extensions stay, and what is in public, but
// for the extensions' own, is dropped in the restore's transaction: the user's tables, views,
// sequences, functions, types and the like, and the user's default privileges there. The dump's
// items that make the schema and the extensions are left out. One transaction, so only with psql: an
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
const inPublic = `SELECT d.classid, d.objid, i.type, i.identity, coalesce((SELECT s.refobjid FROM pg_shdepend s
		WHERE s.dbid = (SELECT oid FROM pg_database WHERE datname = current_database()) AND s.classid = d.classid
		  AND s.objid = d.objid AND s.deptype = 'o' AND s.refclassid = 'pg_authid'::regclass LIMIT 1), 10) AS owner
	FROM pg_depend d CROSS JOIN LATERAL pg_identify_object(d.classid, d.objid, 0) i
	WHERE d.refclassid = 'pg_namespace'::regclass AND d.refobjid = 'public'::regnamespace AND d.deptype = 'n'
	  AND d.classid <> 'pg_extension'::regclass
	  AND NOT EXISTS (SELECT FROM pg_depend x WHERE x.classid = d.classid AND x.objid = d.objid AND x.deptype IN ('e', 'i')
	      AND NOT (x.refclassid = x.classid AND x.refobjid = x.objid))`

// withInPublic are the things that go with what is in public and are another user's, with the
// extensions that would (a member of one that uses it): what depends on it, what that is a part
// of, and so on, as DROP … CASCADE goes. A temporary table (an administrator's open one, of a
// type in public) is not another user's thing to refuse a restore for: it goes when its session
// ends, so it is neither counted nor followed.
var withInPublic = `WITH RECURSIVE top AS (` + inPublic + `), hit(classid, objid, objsubid) AS (
	SELECT classid, objid, 0 FROM top
	UNION
	SELECT n.classid, n.objid, n.objsubid FROM hit h CROSS JOIN LATERAL (
		SELECT d.classid, d.objid, d.objsubid FROM pg_depend d
		WHERE d.refclassid = h.classid AND d.refobjid = h.objid AND (h.objsubid = 0 OR d.refobjsubid = h.objsubid)
		  AND NOT (d.refclassid = 'pg_class'::regclass AND ` + tempRel(`d.refobjid`) + `)
		UNION ALL
		SELECT d.refclassid, d.refobjid, d.refobjsubid FROM pg_depend d
		WHERE d.classid = h.classid AND d.objid = h.objid AND d.objsubid = h.objsubid AND d.deptype IN ('i', 'e')
		  AND NOT (d.classid = 'pg_class'::regclass AND ` + tempRel(`d.objid`) + `)) n)
SELECT DISTINCT i.type || ' ' || i.identity || ' (' || pg_get_userbyid(o.owner) || ')' FROM hit h
CROSS JOIN LATERAL pg_identify_object(h.classid, h.objid, h.objsubid) i
CROSS JOIN LATERAL (SELECT coalesce(
	(SELECT c.relowner FROM pg_class c WHERE c.oid = CASE h.classid WHEN 'pg_class'::regclass THEN h.objid
		WHEN 'pg_rewrite'::regclass THEN (SELECT r.ev_class FROM pg_rewrite r WHERE r.oid = h.objid)
		WHEN 'pg_trigger'::regclass THEN (SELECT t.tgrelid FROM pg_trigger t WHERE t.oid = h.objid)
		WHEN 'pg_constraint'::regclass THEN (SELECT nullif(k.conrelid, 0) FROM pg_constraint k WHERE k.oid = h.objid)
		WHEN 'pg_attrdef'::regclass THEN (SELECT a.adrelid FROM pg_attrdef a WHERE a.oid = h.objid)
		WHEN 'pg_policy'::regclass THEN (SELECT p.polrelid FROM pg_policy p WHERE p.oid = h.objid) END),
	(SELECT t.typowner FROM pg_type t WHERE t.oid = CASE h.classid WHEN 'pg_type'::regclass THEN h.objid
		WHEN 'pg_constraint'::regclass THEN (SELECT k.contypid FROM pg_constraint k WHERE k.oid = h.objid) END),
	(SELECT s.refobjid FROM pg_shdepend s WHERE s.dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
		AND s.classid = h.classid AND s.objid = h.objid AND s.deptype = 'o' AND s.refclassid = 'pg_authid'::regclass LIMIT 1),
	CASE WHEN h.classid IN ('pg_cast'::regclass, 'pg_amop'::regclass, 'pg_amproc'::regclass, 'pg_default_acl'::regclass,
		'pg_publication_rel'::regclass, 'pg_publication_namespace'::regclass) THEN (SELECT oid FROM pg_roles WHERE rolname = current_user) END,
	10) AS owner) o
WHERE (h.classid <> 'pg_class'::regclass OR NOT ` + tempRel(`h.objid`) + `)
  AND (h.classid = 'pg_extension'::regclass OR (NOT pg_has_role(current_user, o.owner, 'USAGE')
	AND NOT EXISTS (SELECT FROM top t WHERE t.classid = h.classid AND t.objid = h.objid)
	-- what another thing has made (a range's functions are the first superuser's) is that thing's
	AND NOT EXISTS (SELECT FROM pg_depend x WHERE x.classid = h.classid AND x.objid = h.objid AND x.deptype = 'i'
		AND NOT (x.refclassid = x.classid AND x.refobjid = x.objid))))
ORDER BY 1`

// tempRel is pg_class's oid being a temporary relation (pg_temp_N / pg_toast_temp_N, or
// one of the temporary kind): withInPublic leaves those out (a session's own, gone when it
// ends). The underscores in the LIKE pattern are escaped so a permanent schema such as
// pgxtemp_data is never matched.
func tempRel(oid string) string {
	return `EXISTS (SELECT FROM pg_catalog.pg_class t WHERE t.oid = ` + oid + `
		AND (t.relpersistence = 't' OR t.relnamespace IN (SELECT oid FROM pg_catalog.pg_namespace
			WHERE nspname LIKE 'pg\_temp\_%' ESCAPE '\' OR nspname LIKE 'pg\_toast\_temp\_%' ESCAPE '\')))`
}

// publicDefaults are the default privileges in public that the user may change (another's are for
// what the other makes, and stay): whose, on what, and whom they are for.
const publicDefaults = `SELECT DISTINCT a.defaclrole AS owner, a.defaclobjtype AS kind, x.grantee
	FROM pg_default_acl a CROSS JOIN LATERAL aclexplode(a.defaclacl) x
	WHERE a.defaclnamespace = 'public'::regnamespace AND pg_has_role(current_user, a.defaclrole, 'USAGE')`

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
	SELECT pg_catalog.array_agg(pg_catalog.format('DROP %s IF EXISTS %s CASCADE', CASE o.type` + kinds.String() + ` END, o.dropname)) INTO stmts
	FROM (SELECT o.type, CASE WHEN o.type = 'aggregate' AND right(o.identity, 2) = '()'
	        THEN pg_catalog.regexp_replace(o.identity, '\(\)$', '(*)') ELSE o.identity END AS dropname
	      FROM (` + inPublic + `) o) o;
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
	if why := cannotHold(list); why != "" {
		return cannot("the backup holds %s: it cannot be put back in the restore's one transaction", why)
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
	if err := t.mayAdd(ctx, d, list, exts); err != nil {
		return h, err
	}
	if schema, err := t.mayMakeElsewhere(ctx, d, list); err != nil {
		return h, err
	} else if schema != "" {
		return cannot("the backup holds things in the schema %s, and the database user %s may make nothing there (a GRANT "+
			"of their own, or CREATE on the database to make the schema): nothing was changed", schema, user)
	}
	if collisions, err := t.existingElsewhere(ctx, d, list); err != nil {
		return h, err
	} else if len(collisions) > 0 {
		return cannot("the backup holds things in another schema that are already there (%s): drop or rename them first, "+
			"nothing was changed", few(collisions))
	}
	// what the user could only be warned of, or refused: the server grants nothing on what is
	// another's, and the restore would be found not whole. The dump's grants on the server's
	// own objects (pg_catalog's, a bundled home's clusterSQL: pg_reload_conf and the like) are
	// left out: this server's are its own, and a grant another server's user may not be given
	// would fail the whole transaction
	mine := withoutCatalogACLs(list)
	if !super {
		if mine, err = t.withoutOthers(ctx, d, mine, func(ext string) bool {
			// one made again as a superuser (a trusted one) is the first superuser's
			return again == "" && exts[ext].goes && exts[ext].needsSuper
		}); err != nil {
			return h, err
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
		if !psql {
			return cannot("the restore runs in one transaction, which takes psql, and there is none beside pg_restore " +
				"(install the PostgreSQL client programs, or set MAKTABA_PG_BIN)")
		}
		h.empty = publicAfresh
		skip["SCHEMA - public"] = true // publicAfresh creates public; keep any ACL - SCHEMA public in the dump
		for name, e := range exts {
			if e.there && !e.goes {
				skip["EXTENSION - "+name], skip["COMMENT - EXTENSION "+name] = true, true
			}
		}
		h.list = withoutItems(mine, skip)
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
	if foreign, err = column(ctx, d, withInPublic); err != nil {
		return h, err
	}
	if len(foreign) > 0 {
		return cannot(again+"what is another's would go with it: %s", few(foreign))
	}
	skipSchema("public", !owns)
	for name, e := range exts {
		if e.there {
			skip["EXTENSION - "+name], skip["COMMENT - EXTENSION "+name] = true, true
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
	h.inside, h.empty, h.list = true, emptyPublic(), withoutItems(mine, skip)
	return h, nil
}

// cannotHold is what a dump holds that the restore's one transaction can never put back
// (Maktaba's own dumps hold none of these): large objects, which pg_restore writes wrapped in
// transactions of its own, so the one transaction's promise does not hold; an event trigger,
// which no transaction can make; a foreign server or its wrapper, which the database's user
// may not make again. Refused up front, nothing is touched for it.
func cannotHold(list string) string {
	for _, e := range pgbundle.ParseTOC(list) {
		switch e.Desc {
		case "BLOB", "BLOBS", "LO":
			return "large objects"
		case "EVENT TRIGGER":
			return "an event trigger, which no transaction can make"
		case "SERVER", "FOREIGN DATA WRAPPER", "USER MAPPING":
			return "a foreign server, which this server's user may not make again"
		}
	}
	return ""
}

// mayMakeElsewhere is the first of the dump's schemas beside public (Maktaba's own dumps hold
// nothing in one) that the database's user may make nothing in: the schema is there and the
// user has no CREATE on it (another's), or it is not there and the dump makes it, which takes
// CREATE on the database. Making those things is what the restore would fail on, deep in its
// one transaction; a superuser is refused nothing.
func (t Target) mayMakeElsewhere(ctx context.Context, d *sql.DB, list string) (string, error) {
	want := map[string]bool{}
	for _, e := range pgbundle.ParseTOC(list) {
		switch e.Schema {
		case "public", "-", "pg_catalog", "information_schema":
		default:
			want[e.Schema] = true
		}
	}
	if len(want) == 0 {
		return "", nil
	}
	ask := make([]string, 0, len(want))
	for s := range want {
		ask = append(ask, s)
	}
	slices.Sort(ask)
	rows, err := d.QueryContext(ctx, `SELECT s FROM unnest($1::name[]) s
		WHERE (EXISTS (SELECT FROM pg_namespace n WHERE n.nspname = s)
		       AND NOT has_schema_privilege(s, 'CREATE'))
		   OR (NOT EXISTS (SELECT FROM pg_namespace n WHERE n.nspname = s)
		       AND NOT has_database_privilege(current_database(), 'CREATE'))
		ORDER BY 1 LIMIT 1`, pq.Array(ask))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var schema string
	if rows.Next() {
		if err := rows.Scan(&schema); err != nil {
			return "", err
		}
	}
	return schema, rows.Err()
}

// elsewhereObjects are the non-public objects a dump creates (relations, types, functions),
// keyed by "<schema> <kind> <canonical-name>" with a human-readable label.
func elsewhereObjects(list string) map[string]string {
	out := map[string]string{}
	for _, e := range pgbundle.ParseTOC(list) {
		switch e.Schema {
		case "public", "-", "pg_catalog", "information_schema":
			continue
		}
		kind, of := grantedOn(e.Desc, e.Name)
		if kind == "" {
			continue
		}
		out[e.Schema+" "+kind+" "+of] = strings.ToLower(e.Desc) + " " + e.Schema + "." + of
	}
	return out
}

// existingElsewhere names any object the dump would create in a non-public schema that is
// already in the database: the restore only empties public, so an existing object in another
// schema collides mid-transaction even for a superuser or schema owner.
func (t Target) existingElsewhere(ctx context.Context, d *sql.DB, list string) ([]string, error) {
	want := elsewhereObjects(list)
	if len(want) == 0 {
		return nil, nil
	}
	schemaSet := map[string]bool{}
	for k := range want {
		s, _, _ := strings.Cut(k, " ")
		schemaSet[s] = true
	}
	schemas := slices.Sorted(maps.Keys(schemaSet))
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path = ''`); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT n.nspname, o.kind, o.name FROM (
			SELECT pronamespace AS ns, 'FUNCTION' AS kind,
				CASE WHEN prokind = 'a' AND pg_catalog.oidvectortypes(proargtypes) = '' THEN pg_catalog.quote_ident(proname) || '(*)'
					ELSE pg_catalog.quote_ident(proname) || '(' || pg_catalog.oidvectortypes(proargtypes) || ')' END AS name FROM pg_catalog.pg_proc
			UNION ALL SELECT relnamespace, 'TABLE', pg_catalog.quote_ident(relname) FROM pg_catalog.pg_class
			UNION ALL SELECT typnamespace, 'TYPE', pg_catalog.quote_ident(typname) FROM pg_catalog.pg_type
				WHERE typtype IN ('c', 'd', 'e', 'r', 'm')) o
		JOIN pg_catalog.pg_namespace n ON n.oid = o.ns
		WHERE n.nspname = ANY($1::name[])`, pq.Array(schemas))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var collisions []string
	seen := map[string]bool{}
	for rows.Next() {
		var schema, kind, name string
		if err := rows.Scan(&schema, &kind, &name); err != nil {
			return nil, err
		}
		k, of := grantedOn(kind, name)
		key := schema + " " + k + " " + of
		if label, ok := want[key]; ok && !seen[label] {
			seen[label] = true
			collisions = append(collisions, label)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Sort(collisions)
	return collisions, nil
}

// withoutCatalogACLs is the dump's list without its grants on the server's own objects
// (pg_catalog's: a bundled home's clusterSQL grants, pg_reload_conf and the like), commented
// out as pg_restore's --use-list wants. This server's grants on them are its own to make, and
// a dump's grant that another server's user may not be given would fail the whole transaction
// (the bundled restore leaves them out the same way, pgbundle's tableOfContents).
func withoutCatalogACLs(list string) string {
	drop := map[string]bool{}
	for _, e := range pgbundle.ParseTOC(list) {
		if e.Desc == "ACL" && e.Schema == "pg_catalog" {
			drop[e.Line] = true
		}
	}
	if len(drop) == 0 {
		return list
	}
	lines := strings.Split(list, "\n")
	for i, line := range lines {
		if drop[strings.TrimRight(line, "\r")] {
			lines[i] = ";" + line
		}
	}
	return strings.Join(lines, "\n")
}

// mayAdd refuses a backup that needs an extension the database lacks and cannot have: it is// tried, and undone (a hosted Postgres may let a user make one its control file says needs a
// superuser, one that has to be preloaded may not load).
func (t Target) mayAdd(ctx context.Context, d *sql.DB, list string, exts map[string]extension) error {
	for _, e := range pgbundle.ParseTOC(list) {
		if e.Desc != "EXTENSION" || exts[e.Name].there {
			continue
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `SET LOCAL search_path = public; SET LOCAL lock_timeout = '2s'`); err == nil {
			_, err = tx.ExecContext(ctx, `CREATE EXTENSION `+pq.QuoteIdentifier(e.Name)+` CASCADE`)
		}
		_ = tx.Rollback()
		var pe *pq.Error
		if errors.As(err, &pe) {
			switch pe.Code {
			case "42710", "42723", "42P07", "42P06", // something of its name is there now: in public, which the restore
				// empties, or outside it (a schema, a foreign-data wrapper, a cast), where the restore itself meets it
				// and commits nothing
				"55P03", "57014", "40P01": // another session in the way, a cancel: it says nothing of what may be made
				continue
			}
			return &cannotRestore{fmt.Sprintf("the backup needs the extension %s, which the database lacks and its user could not "+
				"make (%s): CREATE EXTENSION %s there first, as one who may", e.Name, pe.Message, e.Name)}
		} else if err != nil {
			return err
		}
	}
	return nil
}

// withoutOthers is list without the items a user who is no superuser cannot restore, and that
// change nothing of what is put back: the grants on an extension's own objects that are another's
// (the dump defines everything else it grants on), and the default privileges of users it may
// not act for. lost says of an extension that its objects will be another's once it is made again.
func (t Target) withoutOthers(ctx context.Context, d *sql.DB, list string, lost func(ext string) bool) (string, error) {
	roles, err := column(ctx, d, `SELECT rolname FROM pg_roles WHERE pg_has_role(current_user, oid, 'USAGE')`)
	if err != nil {
		return "", err
	}
	// the members of extensions that are the user's own all the same: queried with
	// search_path = '' and oidvectortypes(proargtypes) so function input types match pg_dump's
	// canonical spelling, then normalized through the same grantedOn/functionSig key as TOC.
	tx, err := d.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path = ''`); err != nil {
		return "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.extname, n.nspname, o.kind, o.name FROM (
			SELECT 'pg_catalog.pg_proc'::regclass AS cls, oid, pronamespace AS ns, proowner AS owner, 'FUNCTION' AS kind,
				CASE WHEN prokind = 'a' AND pg_catalog.oidvectortypes(proargtypes) = '' THEN pg_catalog.quote_ident(proname) || '(*)'
					ELSE pg_catalog.quote_ident(proname) || '(' || pg_catalog.oidvectortypes(proargtypes) || ')' END AS name FROM pg_catalog.pg_proc
			UNION ALL SELECT 'pg_catalog.pg_class'::regclass, oid, relnamespace, relowner, 'TABLE', pg_catalog.quote_ident(relname) FROM pg_catalog.pg_class
			UNION ALL SELECT 'pg_catalog.pg_type'::regclass, oid, typnamespace, typowner, 'TYPE', pg_catalog.quote_ident(typname) FROM pg_catalog.pg_type) o
		JOIN pg_catalog.pg_namespace n ON n.oid = o.ns
		JOIN pg_catalog.pg_depend x ON x.classid = o.cls AND x.objid = o.oid AND x.deptype = 'e' AND x.refclassid = 'pg_catalog.pg_extension'::regclass
		JOIN pg_catalog.pg_extension e ON e.oid = x.refobjid
		WHERE pg_catalog.pg_has_role(current_user, o.owner, 'USAGE')`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	known := map[string]bool{}
	for rows.Next() {
		var ext, schema, kind, name string
		if err := rows.Scan(&ext, &schema, &kind, &name); err != nil {
			return "", err
		}
		if !lost(ext) {
			if k, of := grantedOn(kind, name); k != "" {
				known[schema+" "+k+" "+of] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	toc := pgbundle.ParseTOC(list)
	for _, e := range toc {
		if kind, name := grantedOn(e.Desc, e.Name); kind != "" {
			known[e.Schema+" "+kind+" "+name] = true
		}
	}
	drop := map[string]bool{}
	for _, e := range toc {
		switch e.Desc {
		case "ACL":
			kind, name, _ := strings.Cut(e.Name, " ")
			if kind, name = grantedOn(kind, name); e.Schema != "-" && kind != "" && !known[e.Schema+" "+kind+" "+name] {
				drop[e.Line] = true
			}
		case "DEFAULT ACL":
			drop[e.Line] = !slices.Contains(roles, e.Owner)
		}
	}
	lines := strings.Split(list, "\n")
	for i, l := range lines {
		if drop[strings.TrimRight(l, "\r")] {
			lines[i] = ";" + l
		}
	}
	return strings.Join(lines, "\n"), nil
}

// grantedOn is the kind of thing, and its name, a grant on an item of a dump is on: what a
// function's grant names is the function's signature — its arguments are written two ways (the
// definition's tag carries the argument names, the grant's the types alone), so both are made
// one key here (functionSig), and a function that shares only a name with another does not take
// its grants — and a column's the table, the column's own name taken whole however it is
// spelled, dots included ("" for what has no grants).
func grantedOn(desc, name string) (kind, of string) {
	switch desc {
	case "FUNCTION", "PROCEDURE", "AGGREGATE":
		return "FUNCTION", functionSig(name)
	case "TABLE", "VIEW", "MATERIALIZED VIEW", "FOREIGN TABLE", "SEQUENCE":
		return "TABLE", unquote(name)
	case "COLUMN":
		if table, _, ok := columnOf(name); ok {
			return "TABLE", table
		}
		return "TABLE", unquote(name)
	case "TYPE", "DOMAIN":
		return "TYPE", unquote(name)
	default:
		return "", ""
	}
}

// scanIdent reads one SQL identifier from the start of s: either double-quoted (with ""
// escapes, returning the unquoted value) or bare (stopping at '.', ' ', or '\t').
func scanIdent(s string) (val, rest string, ok bool) {
	if s == "" {
		return "", s, false
	}
	if s[0] == '"' {
		var sb strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == '"' {
				if i+1 < len(s) && s[i+1] == '"' {
					sb.WriteByte('"')
					i++
					continue
				}
				return sb.String(), s[i+1:], true
			}
			sb.WriteByte(s[i])
		}
		return "", s, false
	}
	i := strings.IndexAny(s, ". \t")
	if i < 0 {
		return s, "", true
	}
	if i == 0 {
		return "", s, false
	}
	return s[:i], s[i:], true
}

// columnOf splits a COLUMN ACL tag into its unquoted table and column names: PostgreSQL
// pg_dump writes "<table>.<column>" (with double-quoted identifiers when names contain dots,
// spaces or quotes, e.g. videos."a.b", "my.tbl"."a.b", "a""b.c"."d.e"), and older/space-
// separated forms "<table> <column>" are accepted too. ok is false when name is not a
// two-identifier column reference.
func columnOf(name string) (table, column string, ok bool) {
	rest := strings.TrimSpace(name)
	first, rest, ok1 := scanIdent(rest)
	if !ok1 || first == "" {
		return "", "", false
	}
	switch {
	case strings.HasPrefix(rest, "."):
		rest = rest[1:]
	case len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t'):
		rest = strings.TrimLeft(rest, " \t")
	default:
		return "", "", false
	}
	second, tail, ok2 := scanIdent(rest)
	if !ok2 || second == "" || strings.TrimSpace(tail) != "" {
		return "", "", false
	}
	return first, second, true
}

// functionSig is a function's (or aggregate's, procedure's) name and canonical input argument
// types as one key, matching both PG 16/18 definition tags (types only), ACL tags (which may
// carry argument names, modes, and defaults), and pg_catalog.oidvectortypes(proargtypes).
// Quoted identifier contents are preserved verbatim while whitespace outside quotes is
// normalized.
func functionSig(tag string) string {
	name, args, ok := splitSig(tag)
	if !ok {
		return unquote(tag)
	}
	var sb strings.Builder
	sb.WriteString(unquote(name))
	sb.WriteByte('(')
	for i, arg := range splitArgs(args) {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(argType(arg))
	}
	sb.WriteByte(')')
	return sb.String()
}

// splitSig is a signature's name and its arguments as written (ok false: no parentheses).
func splitSig(tag string) (name, args string, ok bool) {
	if strings.HasPrefix(tag, `"`) {
		for i := 1; i < len(tag); i++ {
			if tag[i] != '"' {
				continue
			}
			if i+1 < len(tag) && tag[i+1] == '"' {
				i++
				continue
			}
			rest := tag[i+1:]
			if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
				return tag[:i+1], rest[1 : len(rest)-1], true
			}
			return "", "", false
		}
		return "", "", false
	}
	i := strings.IndexByte(tag, '(')
	if i < 0 || !strings.HasSuffix(tag, ")") {
		return "", "", false
	}
	return tag[:i], tag[i+1 : len(tag)-1], true
}

// splitArgs is a signature's argument list split at its top-level commas (a default's
// expression may hold commas of its own, inside parentheses or quotes).
func splitArgs(args string) []string {
	var out []string
	depth, dquot, squot, last := 0, false, false, 0
	for i := 0; i < len(args); i++ {
		c := args[i]
		switch {
		case dquot && c == '"':
			if i+1 < len(args) && args[i+1] == '"' {
				i++
				continue
			}
			dquot = false
		case squot && c == '\'':
			if i+1 < len(args) && args[i+1] == '\'' {
				i++
				continue
			}
			squot = false
		case !dquot && !squot && c == '"':
			dquot = true
		case !dquot && !squot && c == '\'':
			squot = true
		case !dquot && !squot && (c == '(' || c == '['):
			depth++
		case !dquot && !squot && (c == ')' || c == ']'):
			depth--
		case !dquot && !squot && c == ',' && depth == 0:
			out = append(out, args[last:i])
			last = i + 1
		}
	}
	if a := strings.TrimSpace(args[last:]); a != "" || len(out) > 0 {
		out = append(out, a)
	}
	return out
}

type sqlTokKind int

const (
	tokWord sqlTokKind = iota
	tokQuoted
	tokPunct
)

type sqlTok struct {
	kind sqlTokKind
	text string
}

// scanArgTokens tokenizes a function argument up to any top-level DEFAULT or '='.
// Quoted identifiers ("...") keep their exact bytes including spaces and case.
func scanArgTokens(arg string) []sqlTok {
	var toks []sqlTok
	depth := 0
	for i := 0; i < len(arg); {
		c := arg[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		if c == '"' {
			start := i
			i++
			for i < len(arg) {
				if arg[i] == '"' {
					if i+1 < len(arg) && arg[i+1] == '"' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			toks = append(toks, sqlTok{kind: tokQuoted, text: arg[start:i]})
			continue
		}
		if c == '\'' {
			// Single-quoted literal (only in DEFAULT expressions); skip cleanly.
			i++
			for i < len(arg) {
				if arg[i] == '\'' {
					if i+1 < len(arg) && arg[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			continue
		}
		if c == '(' || c == '[' {
			depth++
			toks = append(toks, sqlTok{kind: tokPunct, text: arg[i : i+1]})
			i++
			continue
		}
		if c == ')' || c == ']' {
			if depth > 0 {
				depth--
			}
			toks = append(toks, sqlTok{kind: tokPunct, text: arg[i : i+1]})
			i++
			continue
		}
		if c == '.' || c == '*' || c == ',' {
			toks = append(toks, sqlTok{kind: tokPunct, text: arg[i : i+1]})
			i++
			continue
		}
		if c == '=' && depth == 0 {
			break
		}
		start := i
		for i < len(arg) && !strings.ContainsRune(" \t\n\r\"'()[],.*=", rune(arg[i])) {
			i++
		}
		word := arg[start:i]
		if depth == 0 && strings.EqualFold(word, "DEFAULT") {
			break
		}
		toks = append(toks, sqlTok{kind: tokWord, text: strings.ToLower(word)})
	}
	return toks
}

// isMultiWordTypeStart reports whether toks begins with one of PostgreSQL's built-in
// multi-word SQL type names (never a parameter name before another type).
func isMultiWordTypeStart(toks []sqlTok) bool {
	if len(toks) < 2 || toks[0].kind != tokWord || toks[1].kind != tokWord {
		return false
	}
	w0, w1 := toks[0].text, toks[1].text
	switch w0 {
	case "double":
		return w1 == "precision"
	case "character", "bit":
		return w1 == "varying"
	case "timestamp", "time":
		return w1 == "with" || w1 == "without"
	case "interval":
		switch w1 {
		case "year", "month", "day", "hour", "minute", "second":
			return true
		}
	}
	return false
}

// argType is one argument as its canonical input type: leading modes (IN, INOUT, VARIADIC),
// parameter name (bare or quoted), and DEFAULT expression are stripped, while quoted type
// identifiers keep their internal spaces and case verbatim.
func argType(arg string) string {
	toks := scanArgTokens(arg)
	// Strip leading parameter mode keywords (IN, INOUT, VARIADIC).
	for len(toks) > 1 && toks[0].kind == tokWord {
		switch toks[0].text {
		case "in", "inout", "variadic":
			toks = toks[1:]
			continue
		}
		break
	}
	// Strip parameter name if present: an identifier followed by another type token
	// (not '.' for schema.type, not '(' or '[' for type modifier/array, and not a
	// multi-word SQL type start).
	if len(toks) >= 2 && (toks[0].kind == tokWord || toks[0].kind == tokQuoted) &&
		toks[1].text != "." && toks[1].text != "(" && toks[1].text != "[" &&
		!isMultiWordTypeStart(toks) {
		toks = toks[1:]
	}
	var sb strings.Builder
	for i, tk := range toks {
		if i > 0 {
			prev := toks[i-1]
			needSpace := (prev.kind != tokPunct || prev.text == ")") &&
				(tk.kind != tokPunct)
			if needSpace {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(tk.text)
	}
	return sb.String()
}

func unquote(name string) string {
	if len(name) > 1 && name[0] == '"' && name[len(name)-1] == '"' {
		return strings.ReplaceAll(name[1:len(name)-1], `""`, `"`)
	}
	return name
}

// notMine names what is in public that the database user may not drop.
func (t Target) notMine(ctx context.Context, d *sql.DB) ([]string, error) {
	kinds := slices.Sorted(maps.Keys(dropKinds))
	return column(ctx, d, `SELECT o.type || ' ' || o.identity || ' (' || pg_get_userbyid(o.owner) || ')' FROM (`+inPublic+`) o
		WHERE NOT pg_has_role(current_user, o.owner, 'USAGE') OR o.type NOT IN ('`+strings.Join(kinds, "', '")+`') ORDER BY 1`)
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
		// 42501 insufficient_privilege, and 42939: the schema's name is reserved
		// (pg_catalog, where an untrusted extension may sit) — one the user may not make it
		// in either way
		if errors.As(err, &pe) && mayNotMakeCode(pe.Code) {
			return ext, nil
		}
	}
	return "", rows.Err()
}

// mayNotMakeCode is a CREATE EXTENSION error that says the database user may not make the
// extension there: insufficient_privilege (42501), or the schema's name being a reserved one
// (42939) — an untrusted extension in pg_catalog, which only a superuser may make again.
func mayNotMakeCode(code pq.ErrorCode) bool {
	return code == "42501" || code == "42939"
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
