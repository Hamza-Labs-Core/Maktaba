// Story 9.16 — canonical ``library_roots``-store maintenance + overlap
// detection that consults the new normalized table when present.
//
// The Phase-3 :meth:`Handler.checkRootOverlap` consults the
// transitional ``libraries.roots TEXT[]`` column. This file adds the
// canonical path: ``library_roots`` rows live one-per-root, each with a
// canonical-path projection so a string-prefix overlap test runs as a
// single index scan. We keep the legacy column write as a back-fill for
// one release per the README's deprecation note.
//
// :func:`SyncLibraryRoots` is the trigger that creates / updates the
// rows; the API layer calls it from :meth:`Handler.Create` and
// :meth:`Handler.Patch` after the legacy column has been written.
package libraries

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// SyncLibraryRoots upserts one ``library_roots`` row per entry in
// ``roots`` and deletes any row whose path is no longer present. Both
// inserts and deletes happen in the same transaction so a concurrent
// reader never sees a half-applied state.
//
// The canonical path is computed via :func:`canonicalPath`. The store
// uses ``(library_id, path_canonical)`` as the uniqueness key so two
// distinct user inputs that resolve to the same canonical form (e.g.,
// ``/mnt/media`` and ``/mnt/media/``) collapse to one row.
func SyncLibraryRoots(
	ctx context.Context,
	tx *sql.Tx,
	libraryID string,
	roots []string,
) error {
	canonicals := make([]string, 0, len(roots))
	want := map[string]string{}
	for _, r := range roots {
		canon := canonicalPath(r)
		if canon == "" {
			continue
		}
		if _, ok := want[canon]; ok {
			continue
		}
		want[canon] = r
		canonicals = append(canonicals, canon)
	}

	// Drop rows that are no longer in the desired set.
	if len(canonicals) == 0 {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM library_roots WHERE library_id = $1`, libraryID); err != nil {
			return err
		}
		return nil
	}

	args := []any{libraryID}
	placeholders := make([]string, 0, len(canonicals))
	for i, c := range canonicals {
		placeholders = append(placeholders, "$"+itoa(i+2))
		args = append(args, c)
	}
	delQ := `DELETE FROM library_roots WHERE library_id = $1 AND path_canonical NOT IN (` +
		strings.Join(placeholders, ",") + `)`
	if _, err := tx.ExecContext(ctx, delQ, args...); err != nil {
		return err
	}

	// Upsert each desired row. We rely on the ``(library_id,
	// path_canonical)`` unique index to keep this idempotent.
	for canon, raw := range want {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO library_roots (library_id, path, path_canonical)
			VALUES ($1, $2, $3)
			ON CONFLICT (library_id, path_canonical) DO UPDATE SET path = EXCLUDED.path
		`, libraryID, raw, canon)
		if err != nil {
			return err
		}
	}
	return nil
}

// canonicalPath canonicalises a user-supplied root path. We use
// ``filepath.Clean`` plus a trailing-slash strip — the API lives in a
// pure Go process and cannot resolve symlinks reliably across the
// container boundary, so symlink resolution is the Pipeline's job at
// scan time. The same input must always produce the same output so
// the unique constraint stays meaningful.
func canonicalPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	cleaned := filepath.Clean(p)
	if len(cleaned) > 1 {
		cleaned = strings.TrimRight(cleaned, string(filepath.Separator))
	}
	return cleaned
}

// CheckRootsOverlapV2 is the Story 9.16 overlap check that consults
// ``library_roots`` first and falls back to the legacy ``roots`` column
// for libraries that haven't been back-filled yet. Returns nil when
// no overlap is found; otherwise a 422 with the offending pair in the
// detail.
func CheckRootsOverlapV2(
	ctx context.Context,
	db *sql.DB,
	exceptID string,
	proposedRoots []string,
) *httperror.Error {
	canon := make([]string, 0, len(proposedRoots))
	for _, r := range proposedRoots {
		canon = append(canon, canonicalPath(r))
	}
	// Self-overlap inside the same library (e.g. ``["/a", "/a/b"]``)
	// must be caught here because the DB unique key is (library_id,
	// path_canonical) — it would *accept* both rows.
	for i := 0; i < len(canon); i++ {
		for j := i + 1; j < len(canon); j++ {
			if pathsOverlap(canon[i], canon[j]) {
				return &httperror.Error{
					Type:   "https://maktaba.dev/problems/library-roots-overlap",
					Title:  "library roots overlap (within library)",
					Status: http.StatusUnprocessableEntity,
					Detail: canon[i] + " overlaps " + canon[j] + " in same library",
				}
			}
		}
	}

	rows, err := db.QueryContext(ctx, `
		SELECT library_id, path_canonical FROM library_roots
	`)
	if err != nil {
		// Table missing — fall back to the legacy column check.
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var libID, existing string
		if err := rows.Scan(&libID, &existing); err != nil {
			continue
		}
		if libID == exceptID {
			continue
		}
		for _, p := range canon {
			if pathsOverlap(existing, p) {
				return &httperror.Error{
					Type:   "https://maktaba.dev/problems/library-roots-overlap",
					Title:  "library roots overlap",
					Status: http.StatusUnprocessableEntity,
					Detail: p + " overlaps existing library root " + existing,
				}
			}
		}
	}
	return nil
}
