package paginate

import (
	"net/url"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Story 7.2 AC-3: limit is in [1,200]; default 50; anything else is a
// 400 with type invalid-query-parameter.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// ParseLimit reads the ?limit= query parameter. Returns the bounded
// integer or an *httperror.Error suitable for Write. Missing param →
// DefaultLimit. Out-of-range or non-numeric → 400.
func ParseLimit(q url.Values) (int, *httperror.Error) {
	raw := q.Get("limit")
	if raw == "" {
		return DefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, httperror.InvalidQuery("limit must be an integer")
	}
	if n < 1 || n > MaxLimit {
		return 0, httperror.InvalidQuery("limit must be in [1,200]")
	}
	return n, nil
}
