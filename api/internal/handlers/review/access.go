package review

import (
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// CanReview is the one rule of every word review route: a home admin. A library's
// write or admin role (library_acl) grants nothing here, and a cloud-linked sign-in is
// never an admin (its principal has IsAdmin cleared) unless the account itself is one.
func CanReview(p *principal.Principal) bool { return p != nil && p.IsAdmin }

// gate answers 401 without a session and 403 for anyone but a home admin; true when the
// caller may go on.
func gate(w http.ResponseWriter, r *http.Request) (*principal.Principal, bool) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, &httperror.Error{
			Type: "https://maktaba.tv/problems/unauthorized", Title: "unauthorized",
			Status: http.StatusUnauthorized, Detail: "sign in",
		})
		return nil, false
	}
	if !CanReview(p) {
		httperror.Write(w, r, httperror.Forbidden("https://maktaba.tv/problems/review-admin-only",
			"only home admins review words"))
		return nil, false
	}
	return p, true
}
