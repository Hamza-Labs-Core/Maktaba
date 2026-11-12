// Package libaccess answers whether Maktaba may write a library (Epic 59, story 59.03): the
// result of the library's last access check (libraries.access), which the API's writing
// actions consult before they touch a file. A read-only library is a supported state, not an
// error: convert, dedup, YouTube downloads into it, repair and delete are refused for it with
// 409 library_read_only and the reason, and nothing is touched.
package libaccess

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Access values (libraries.access); "" is a library never checked, which is writable.
const (
	ReadWrite = "rw"
	ReadOnly  = "ro"
	None      = "none"
)

// Problem types and codes of the refusals.
var (
	TypeReadOnly = httperror.Type("library-read-only")
	TypeNoAccess = httperror.Type("library-no-access")
)

const (
	CodeReadOnly = "library_read_only"
	CodeNoAccess = "library_no_access"
	// DetailReadOnly is library.read_only's English (the web shows its own words for the code).
	DetailReadOnly = "This library is read-only for Maktaba"
)

// Querier is a *sql.DB or *sql.Tx.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Writable reports whether access lets Maktaba write: rw, or never checked.
func Writable(access string) bool { return access != ReadOnly && access != None }

// Of is a library's access: "" for one never checked, or no such library.
func Of(ctx context.Context, q Querier, libraryID string) (string, error) {
	var a sql.NullString
	err := q.QueryRowContext(ctx, `SELECT access FROM libraries WHERE id = $1`, libraryID).Scan(&a)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return a.String, nil
}

// OfVideo is the access of a video's library ("" when the video or its library is gone).
func OfVideo(ctx context.Context, q Querier, videoID string) (string, error) {
	var a sql.NullString
	err := q.QueryRowContext(ctx, `SELECT l.access FROM videos v JOIN libraries l ON l.id = v.library_id
		WHERE v.id = $1`, videoID).Scan(&a)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return a.String, nil
}

// Problem is the 409 a writing action answers on a read-only library.
func Problem() *httperror.Error {
	return (&httperror.Error{Type: TypeReadOnly, Title: "library read-only", Status: http.StatusConflict,
		Detail: DetailReadOnly}).With("code", CodeReadOnly)
}

// NoAccessProblem is the 409 a scan answers on a library whose roots cannot be listed.
func NoAccessProblem() *httperror.Error {
	return (&httperror.Error{Type: TypeNoAccess, Title: "library not accessible", Status: http.StatusConflict,
		Detail: "Maktaba cannot list this library's folders"}).With("code", CodeNoAccess)
}

// Guard answers 409 library_read_only (and false) when the library is read-only for
// Maktaba, 500 when its access cannot be read; true lets the action go on.
func Guard(w http.ResponseWriter, r *http.Request, q Querier, libraryID string) bool {
	a, err := Of(r.Context(), q, libraryID)
	return guard(w, r, a, err)
}

// GuardVideo is Guard for the library of a video.
func GuardVideo(w http.ResponseWriter, r *http.Request, q Querier, videoID string) bool {
	a, err := OfVideo(r.Context(), q, videoID)
	return guard(w, r, a, err)
}

func guard(w http.ResponseWriter, r *http.Request, access string, err error) bool {
	if err != nil {
		httperror.Write(w, r, httperror.Internal("library access").Wrap(err))
		return false
	}
	if !Writable(access) {
		httperror.Write(w, r, Problem())
		return false
	}
	return true
}
