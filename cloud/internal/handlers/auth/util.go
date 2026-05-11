package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": code, "message": msg})
}

func userViewFrom(u stores.User) userView {
	v := userView{
		ID:            u.ID,
		Email:         u.Email,
		EmailVerified: u.EmailVerified,
		Plan:          u.Plan,
	}
	if u.DisplayName.Valid {
		v.DisplayName = u.DisplayName.String
	}
	return v
}

func setRefreshCookie(w http.ResponseWriter, value, domain string, secure bool, expires time.Time) {
	c := &http.Cookie{
		Name:     "maktaba_refresh",
		Value:    value,
		Path:     "/v1/auth",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	}
	http.SetCookie(w, c)
}

func clearRefreshCookie(w http.ResponseWriter, domain string, secure bool) {
	c := &http.Cookie{
		Name:     "maktaba_refresh",
		Value:    "",
		Path:     "/v1/auth",
		Domain:   domain,
		HttpOnly: true,
		Secure:   secure,
		MaxAge:   -1,
	}
	http.SetCookie(w, c)
}

// isUniqueViolation matches the lib/pq error string. We don't import
// pq.Error directly here to keep this package decoupled from the
// driver — handlers above only check the boolean.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "duplicate key") || strings.Contains(s, "unique constraint")
}
