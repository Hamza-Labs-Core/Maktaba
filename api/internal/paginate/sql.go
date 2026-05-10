package paginate

import (
	"fmt"
	"strconv"

	"github.com/google/uuid"
)

// IDKind tells Where how to coerce the cursor's opaque ID into a
// SQL argument. UUID-keyed tables (videos, libraries, ...) pass
// IDKindUUID; bigint-keyed tables (segments, jobs, ...) pass
// IDKindBigint.
type IDKind int

const (
	IDKindUUID IDKind = iota
	IDKindBigint
)

// Where returns the SQL fragment + args needed to resume a paginated
// list from the given cursor. The fragment uses Postgres-style
// placeholders ($n); SQLite call sites translate via the project's
// dialect helper.
//
// When the cursor is empty (first page), an empty fragment + nil args
// is returned so the caller can append it unconditionally.
//
// The form is:
//
//	(time_col <op> $n  OR  (time_col = $n  AND  id_col <op> $n+1))
//
// `op` is `<` for DESC sorts (the common case), `>` for ASC. Tuple
// comparisons (`(a, b) < ($1, $2)`) would be cleaner but Postgres
// requires parens and SQLite's row-value support is patchier than the
// docs suggest — the OR form works on both.
//
// `nextPlaceholder` is the next free $n in the caller's argument list;
// we use it and `nextPlaceholder+1`. This keeps the caller free to
// append cursor params after their other filters without renumbering.
func Where(spec SortSpec, cur Cursor, nextPlaceholder int, idKind IDKind) (frag string, args []any) {
	if cur.ID == "" {
		return "", nil
	}
	op := "<"
	if !spec.Desc {
		op = ">"
	}
	p1 := fmt.Sprintf("$%d", nextPlaceholder)
	p2 := fmt.Sprintf("$%d", nextPlaceholder+1)
	frag = fmt.Sprintf("(%s %s %s OR (%s = %s AND %s %s %s))",
		spec.TimeCol, op, p1,
		spec.TimeCol, p1,
		spec.IDCol, op, p2)

	var idArg any
	switch idKind {
	case IDKindUUID:
		u, err := uuid.Parse(cur.ID)
		if err != nil {
			// Decode validates the cursor up to the JSON shape, but the
			// ID is opaque to the cursor primitive. A cursor minted for
			// a different endpoint can land here with a non-UUID ID;
			// drop the predicate so the query degrades to "first page"
			// instead of returning a 500.
			return "", nil
		}
		idArg = u
	case IDKindBigint:
		i, err := strconv.ParseInt(cur.ID, 10, 64)
		if err != nil {
			return "", nil
		}
		idArg = i
	}
	args = []any{cur.Updated, idArg}
	return
}
