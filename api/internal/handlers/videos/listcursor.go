package videos

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// listCursor is the opaque `cursor` of GET /api/videos: the sort key and id
// of the last row on the previous page. The next page starts strictly after
// that (key, id) pair in the list's order (sort DESC, id DESC), so a page
// never repeats or skips a row even when many rows share the same key.
type listCursor struct {
	Sort string `json:"s"` // the sort the cursor was minted for
	Key  string `json:"k"` // RFC3339Nano for the time sorts, the title otherwise
	ID   string `json:"i"`
}

// sortExpr is the ORDER BY expression for a sort field. Title sorts treat a
// missing title as "" so the order and the cursor comparison agree on NULLs.
func sortExpr(sort string) string {
	if sort == "title" {
		return "COALESCE(title, '')"
	}
	return sort
}

func encodeListCursor(sort string, v Video) string {
	c := listCursor{Sort: sort, ID: v.ID}
	switch sort {
	case "created_at":
		c.Key = v.CreatedAt.UTC().Format(time.RFC3339Nano)
	case "title":
		if v.Title != nil {
			c.Key = *v.Title
		}
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

// decodeListCursor parses a cursor for the given sort. An empty string is the
// first page (nil cursor, no error).
func decodeListCursor(raw, sort string) (*listCursor, *httperror.Error) {
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
	if c.Sort != sort {
		return nil, invalidCursor("cursor was made for a different sort")
	}
	return &c, nil
}

// where returns the predicate that resumes after the cursor, using $n and
// $n+1, and its arguments.
func (c *listCursor) where(sort string, n int) (string, []any, *httperror.Error) {
	var key any = c.Key
	if sort != "title" {
		t, err := time.Parse(time.RFC3339Nano, c.Key)
		if err != nil {
			return "", nil, invalidCursor("malformed cursor time")
		}
		key = t
	}
	col := sortExpr(sort)
	frag := "(" + col + " < " + pidx(n) + " OR (" + col + " = " + pidx(n) + " AND id < " + pidx(n+1) + "))"
	return frag, []any{key, c.ID}, nil
}
