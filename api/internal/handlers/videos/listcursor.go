package videos

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// listCursor is the opaque `cursor` of GET /api/videos: the sort key and id
// of the last row on the previous page. The next page starts strictly after
// that (key, id) pair in the list's order (the sort's key then id, both in
// the list's direction), so a page never repeats or skips a row even when
// many rows share the same key.
type listCursor struct {
	Sort  string `json:"s"`           // the sort the cursor was minted for
	Order string `json:"o,omitempty"` // "asc" or "desc"; "" is desc (cursors made before order existed)
	Key   string `json:"k"`           // RFC3339Nano for the time sorts, a number or the title otherwise
	Null  bool   `json:"n,omitempty"` // the last row's key was NULL (nullable sorts only)
	ID    string `json:"i"`
}

type keyKind int

const (
	keyTime keyKind = iota
	keyText
	keyFloat
	keyInt
)

// listSort is one sort of GET /api/videos. A nullable key sorts its NULLs
// last in both directions, so a list never opens on rows with no value.
type listSort struct {
	expr     string
	kind     keyKind
	nullable bool
}

var listSorts = map[string]listSort{
	"updated_at": {expr: "updated_at", kind: keyTime},
	"created_at": {expr: "created_at", kind: keyTime},
	// A missing title is "" so the order and the cursor agree on NULLs.
	"title": {expr: "COALESCE(title, '')", kind: keyText},
	// The publish date is the file's mtime (yt-dlp sets it to the upload date).
	"published_at": {expr: "mtime", kind: keyTime, nullable: true},
	"duration_sec": {expr: "duration_sec", kind: keyFloat, nullable: true},
	// Everyone's plays (watch_sessions rows), from the join List adds.
	"plays": {expr: "COALESCE(wp.plays, 0)", kind: keyInt},
}

// listSortNames is the order the 400 names the sorts in.
const listSortNames = "updated_at, created_at, title, published_at, duration_sec, plays"

// sortExpr is the ORDER BY expression for a sort field.
func sortExpr(sort string) string {
	return listSorts[sort].expr
}

// orderBy is the ORDER BY list for a sort and direction: NULL keys last,
// then the key, then the id as the tie-break.
func orderBy(sort, order string) string {
	s := listSorts[sort]
	dir := " DESC"
	if order == "asc" {
		dir = " ASC"
	}
	out := s.expr + dir + ", id" + dir
	if s.nullable {
		// false sorts before true on both Postgres and SQLite (0 < 1).
		out = "(" + s.expr + " IS NULL) ASC, " + out
	}
	return out
}

func encodeListCursor(sort, order string, v Video) string {
	c := listCursor{Sort: sort, Order: order, ID: v.ID}
	switch sort {
	case "created_at":
		c.Key = v.CreatedAt.UTC().Format(time.RFC3339Nano)
	case "title":
		if v.Title != nil {
			c.Key = *v.Title
		}
	case "published_at":
		if v.PublishedAt == nil {
			c.Null = true
		} else {
			c.Key = v.PublishedAt.UTC().Format(time.RFC3339Nano)
		}
	case "duration_sec":
		if v.DurationSec == nil {
			c.Null = true
		} else {
			c.Key = strconv.FormatFloat(*v.DurationSec, 'g', -1, 64)
		}
	case "plays":
		var n int64
		if v.Plays != nil {
			n = *v.Plays
		}
		c.Key = strconv.FormatInt(n, 10)
	default:
		c.Key = v.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func invalidCursor(detail string) *httperror.Error {
	return &httperror.Error{
		Type:   httperror.TypeInvalidCursor,
		Title:  "invalid cursor",
		Status: http.StatusBadRequest,
		Detail: detail,
	}
}

// decodeListCursor parses a cursor for the given sort and order. An empty
// string is the first page (nil cursor, no error).
func decodeListCursor(raw, sort, order string) (*listCursor, *httperror.Error) {
	if raw == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, invalidCursor("invalid cursor encoding")
	}
	var c listCursor
	if err := json.Unmarshal(b, &c); err != nil || c.ID == "" {
		return nil, invalidCursor("malformed cursor")
	}
	// videos.id is a UUID column: a bad id must be a 400 here, not a
	// database error.
	if _, err := uuid.Parse(c.ID); err != nil {
		return nil, invalidCursor("malformed cursor")
	}
	if c.Sort != sort {
		return nil, invalidCursor("cursor was made for a different sort")
	}
	if c.Order == "" {
		c.Order = "desc"
	}
	if c.Order != order {
		return nil, invalidCursor("cursor was made for a different order")
	}
	if c.Null && !listSorts[sort].nullable {
		return nil, invalidCursor("malformed cursor")
	}
	return &c, nil
}

// parseKey turns the cursor's key into the parameter the sort compares with.
func (c *listCursor) parseKey(kind keyKind) (any, error) {
	switch kind {
	case keyTime:
		return time.Parse(time.RFC3339Nano, c.Key)
	case keyFloat:
		return strconv.ParseFloat(c.Key, 64)
	case keyInt:
		return strconv.ParseInt(c.Key, 10, 64)
	default:
		return c.Key, nil
	}
}

// where returns the predicate that resumes after the cursor, using $n and
// $n+1, and its arguments.
func (c *listCursor) where(sort, order string, n int) (string, []any, *httperror.Error) {
	s := listSorts[sort]
	op := " < "
	if order == "asc" {
		op = " > "
	}
	if c.Null {
		// The previous page ended among the NULLs, which come last: only
		// NULL rows remain, in id order.
		return "(" + s.expr + " IS NULL AND id" + op + pidx(n) + ")", []any{c.ID}, nil
	}
	key, err := c.parseKey(s.kind)
	if err != nil {
		return "", nil, invalidCursor(fmt.Sprintf("malformed cursor key for %s", sort))
	}
	col := s.expr
	frag := "(" + col + op + pidx(n) + " OR (" + col + " = " + pidx(n) + " AND id" + op + pidx(n+1) + "))"
	if s.nullable {
		// A comparison with NULL is never true, so the NULL rows, which
		// follow every row with a value, are added back.
		frag = "(" + frag + " OR " + col + " IS NULL)"
	}
	return frag, []any{key, c.ID}, nil
}

// validOrder normalises ?order=: "" is desc.
func validOrder(raw string) (string, bool) {
	switch strings.ToLower(raw) {
	case "", "desc":
		return "desc", true
	case "asc":
		return "asc", true
	}
	return "", false
}
