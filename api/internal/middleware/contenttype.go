package middleware

import (
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// ContentTypeJSON enforces application/json (or application/graphql+json
// for the GraphQL endpoint) on mutating methods. GET/HEAD/DELETE/OPTIONS
// have no body and so pass through unchecked. Story 7.19 AC-2.
//
// `application/json; charset=utf-8` is accepted — the parameter is
// stripped before the strict comparison.
func ContentTypeJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodDelete, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		ct := stripParams(r.Header.Get("Content-Type"))
		if ct != "application/json" && ct != "application/graphql+json" {
			httperror.Write(w, r, &httperror.Error{
				Type:   httperror.TypeUnsupportedMediaType,
				Title:  "unsupported media type",
				Status: http.StatusUnsupportedMediaType,
				Detail: "expected application/json",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func stripParams(s string) string {
	if i := strings.Index(s, ";"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.ToLower(s))
}
