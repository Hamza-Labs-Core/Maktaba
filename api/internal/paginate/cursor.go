// Package paginate implements the API's single cursor-pagination
// contract (Story 7.2). Every list endpoint MUST surface paging via
// this primitive — `page` / `offset` is forbidden because the
// underlying lists shift under the user's feet (new videos arrive,
// jobs change state).
//
// Cursor on the wire: base64url, no padding, of a JSON `{u,i,v}` blob.
// `u` is the time component (RFC3339Nano), `i` is the secondary id, and
// `v` is the cursor schema version. Versioning lets a future v2
// reject silently rather than mis-decode against v1 code.
package paginate

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// CurrentVersion is the cursor schema version stamped into every
// cursor encoded by this package. Bumping it is a breaking change for
// already-issued cursors; the test suite guards against silent bumps.
const CurrentVersion = 1

// Cursor is the wire format. Field tags are short to keep the base64
// string under 128 bytes (Story 7.2 AC-1). ID is opaque to this
// package so a single cursor type works for UUID-keyed endpoints
// (videos, libraries, ...) and bigint-keyed endpoints (segments,
// jobs, ...). The endpoint's SortSpec parameterises the SQL
// translation in Where.
type Cursor struct {
	Updated time.Time `json:"u"`
	ID      string    `json:"i"`
	Version int       `json:"v"`
}

// SortSpec describes the ordering of a list endpoint. Today every
// endpoint uses (updated_at DESC, id DESC); the spec exists so a
// future endpoint can swap to ASC or to a different secondary column
// without forking Encode/Decode.
type SortSpec struct {
	TimeCol string
	IDCol   string
	Desc    bool
}

// Encode renders c to its over-the-wire base64url form. The Version
// field is stamped to CurrentVersion regardless of the input value so
// a caller can pass a zero-valued Cursor without thinking about it.
func Encode(c Cursor) string {
	c.Version = CurrentVersion
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Decode parses raw into a Cursor. An empty raw string returns the
// zero Cursor and nil — that's the "first page" sentinel.
//
// Returns an *httperror.Error so the handler can pipe straight into
// httperror.Write without translating internal errors. Story 7.2
// pins three failure modes:
//
//   - bad base64 → 400 invalid-cursor
//   - bad JSON inside the base64 → 400 invalid-cursor
//   - version > CurrentVersion → 400 cursor-unsupported-version
func Decode(raw string) (Cursor, *httperror.Error) {
	if raw == "" {
		return Cursor{}, nil
	}
	bytes, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, &httperror.Error{
			Type:   httperror.TypeInvalidCursor,
			Title:  "invalid cursor",
			Status: http.StatusBadRequest,
			Detail: "invalid cursor encoding",
		}
	}
	var c Cursor
	if err := json.Unmarshal(bytes, &c); err != nil {
		return Cursor{}, &httperror.Error{
			Type:   httperror.TypeInvalidCursor,
			Title:  "invalid cursor",
			Status: http.StatusBadRequest,
			Detail: "invalid cursor body",
		}
	}
	if c.Version > CurrentVersion {
		return Cursor{}, &httperror.Error{
			Type:   httperror.TypeCursorUnsupported,
			Title:  "unsupported cursor version",
			Status: http.StatusBadRequest,
			Detail: "this API version cannot decode this cursor",
		}
	}
	if c.Version < 1 || c.ID == "" {
		return Cursor{}, &httperror.Error{
			Type:   httperror.TypeInvalidCursor,
			Title:  "invalid cursor",
			Status: http.StatusBadRequest,
			Detail: "malformed cursor",
		}
	}
	return c, nil
}
