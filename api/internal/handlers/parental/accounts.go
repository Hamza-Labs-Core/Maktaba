// Package parental serves restricted accounts and their guardians (Epic 57 story
// 57.02): who is restricted, at what limit, who looks after them, and their allow
// and block rules. Every role rule is decided here, on the server:
//
//	read a child's policy and rules     admin, that child's guardian; anyone else 404
//	change the limit, add or drop rules admin, that child's guardian
//	lift the restriction                admin
//	name or remove a guardian           admin
//
// A caller who is no one's guardian gets 403 on a write; a guardian of another
// child gets 404, as for a missing account, so a child's existence is not shown.
// The child reads only their own limit, at GET /api/me/parental.
package parental

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	parentalpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Problem types. Each answer also carries `code`, the short name the apps map to
// their own words (parental.error.* in story 57.07).
const (
	TypeRestricted         = "https://maktaba.tv/problems/restricted"
	TypeRestrictedAdmin    = "https://maktaba.tv/problems/restricted-admin"
	TypeRestrictedGuardian = "https://maktaba.tv/problems/restricted-guardian"
	TypeNoLibraryAccess    = "https://maktaba.tv/problems/no-library-access"
	TypeAdminOnly          = "https://maktaba.tv/problems/admin-only"
	TypePolicyStale        = "https://maktaba.tv/problems/parental-policy-stale"
	TypeCloudSession       = "https://maktaba.tv/problems/cloud-session"
)

// Handler serves the routes. Cache may be nil (reads then go to the store).
type Handler struct {
	Store *parentalpkg.Store
	Cache *parentalpkg.Cache
}

// Mount attaches the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/parental/children", h.Children)
	r.Get("/api/me/parental", h.Me)
	r.Get("/api/users/{id}/parental", h.Get)
	r.Put("/api/users/{id}/parental", h.Put)
	r.Delete("/api/users/{id}/parental", h.Lift)
	r.Post("/api/users/{id}/parental/guardians", h.AddGuardian)
	r.Delete("/api/users/{id}/parental/guardians/{gid}", h.RemoveGuardian)
	r.Get("/api/users/{id}/parental/rules", h.Rules)
	r.Post("/api/users/{id}/parental/rules", h.AddRule)
	r.Delete("/api/users/{id}/parental/rules/{rid}", h.RemoveRule)
}

func unauthorized() *httperror.Error {
	return &httperror.Error{Type: "about:blank", Title: "unauthorized", Status: http.StatusUnauthorized,
		Detail: "authentication required"}
}

func notFound() *httperror.Error { return httperror.NotFound("restricted account") }

func adminOnly() *httperror.Error {
	return httperror.Forbidden(TypeAdminOnly, "only an admin can do this").With("code", "admin_only")
}

// Restricted is the 403 a restricted account gets where it may not act.
func Restricted() *httperror.Error {
	return httperror.Forbidden(TypeRestricted, "a restricted account cannot do this").With("code", "restricted")
}

func (h *Handler) caller(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, unauthorized())
		return nil
	}
	return p
}

// reader lets an admin or the child's guardian through; anyone else gets 404.
func (h *Handler) reader(w http.ResponseWriter, r *http.Request, id string) (parentalpkg.Access, bool) {
	p := h.caller(w, r)
	if p == nil {
		return parentalpkg.AccessNone, false
	}
	access, err := h.Store.AccessTo(r.Context(), p, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental access").Wrap(err))
		return parentalpkg.AccessNone, false
	}
	if access == parentalpkg.AccessNone {
		httperror.Write(w, r, notFound())
		return parentalpkg.AccessNone, false
	}
	return access, true
}

// refuse answers a caller with no access to id who tried to change it: 404 for a
// guardian of another child, 403 for anyone else (`restricted` for a restricted
// account).
func (h *Handler) refuse(w http.ResponseWriter, r *http.Request, p *principal.Principal) {
	other, err := h.Store.HasChildren(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental access").Wrap(err))
		return
	}
	if other {
		httperror.Write(w, r, notFound())
		return
	}
	restricted, err := h.Store.IsRestricted(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental access").Wrap(err))
		return
	}
	if restricted {
		httperror.Write(w, r, Restricted())
		return
	}
	httperror.Write(w, r, httperror.Forbidden("", "only an admin or this account's guardian can do this"))
}

// manager lets an admin or the child's guardian change id's policy. A sign-in
// through Maktaba Cloud that may only play reads and does not write.
func (h *Handler) manager(w http.ResponseWriter, r *http.Request, id string) (*principal.Principal, bool) {
	p := h.caller(w, r)
	if p == nil {
		return nil, false
	}
	access, err := h.Store.AccessTo(r.Context(), p, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental access").Wrap(err))
		return nil, false
	}
	if access == parentalpkg.AccessNone {
		h.refuse(w, r, p)
		return nil, false
	}
	if p.CloudScope == principal.CloudScopePlay {
		httperror.Write(w, r, httperror.Forbidden(TypeCloudSession,
			"this sign-in through Maktaba Cloud can only play; sign in on the home to change a limit"))
		return nil, false
	}
	return p, true
}

// admin lets only an admin through. guardianSees404 is for a route where a
// guardian of another child must not learn the account exists (lifting): they get
// 404; the child's own guardian, and everyone else, 403 (`restricted` for a
// restricted account).
func (h *Handler) admin(w http.ResponseWriter, r *http.Request, id string, guardianSees404 bool) (*principal.Principal, bool) {
	p := h.caller(w, r)
	if p == nil {
		return nil, false
	}
	if p.IsAdmin {
		return p, true
	}
	if guardianSees404 {
		mine, err := h.Store.IsGuardian(r.Context(), p.UserID, id)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("parental access").Wrap(err))
			return nil, false
		}
		if !mine {
			h.refuse(w, r, p)
			return nil, false
		}
	}
	// The refusal is certain here; only its reason is read from the database, so a
	// failed read still refuses.
	if restricted, err := h.Store.IsRestricted(r.Context(), p.UserID); err == nil && restricted {
		httperror.Write(w, r, Restricted())
		return nil, false
	}
	httperror.Write(w, r, adminOnly())
	return nil, false
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, parentalpkg.ErrNotRestricted), errors.Is(err, parentalpkg.ErrUserNotFound):
		httperror.Write(w, r, notFound())
	case errors.Is(err, parentalpkg.ErrRuleNotFound):
		httperror.Write(w, r, httperror.NotFound("rule"))
	case errors.Is(err, parentalpkg.ErrGuardianNotFound):
		httperror.Write(w, r, httperror.NotFound("guardian"))
	case errors.Is(err, parentalpkg.ErrStale):
		httperror.Write(w, r, httperror.Conflict(TypePolicyStale,
			"the policy changed since it was read; read it again").With("code", "stale"))
	case errors.Is(err, parentalpkg.ErrRestrictedAdmin):
		httperror.Write(w, r, httperror.Conflict(TypeRestrictedAdmin,
			"a restricted account cannot be an admin").With("code", "restricted_admin"))
	case errors.Is(err, parentalpkg.ErrRestrictedGuardian), errors.Is(err, parentalpkg.ErrSelfGuardian):
		httperror.Write(w, r, httperror.Conflict(TypeRestrictedGuardian,
			"a restricted account cannot look after another account").With("code", "restricted_guardian"))
	case errors.Is(err, parentalpkg.ErrNoLibraryAccess):
		httperror.Write(w, r, (&httperror.Error{Type: TypeNoLibraryAccess, Title: "unprocessable",
			Status: http.StatusUnprocessableEntity,
			Detail: "this account cannot open that library, so it cannot be allowed"}).With("code", "no_library_access"))
	default:
		httperror.Write(w, r, httperror.Internal("parental store").Wrap(err))
	}
}

type childrenResponse struct {
	Children []parentalpkg.Child `json:"children"`
}

// Children implements GET /api/parental/children: every restricted account for an
// admin, a guardian's own children, and an empty list for anyone else.
func (h *Handler) Children(w http.ResponseWriter, r *http.Request) {
	p := h.caller(w, r)
	if p == nil {
		return
	}
	list, err := h.Store.Children(r.Context(), p.UserID, p.IsAdmin)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, childrenResponse{Children: list})
}

type policyResponse struct {
	Policy *parentalpkg.Policy `json:"policy"`
	// Access is what the caller may do: "admin" (everything) or "guardian" (the
	// limit and the rules; not lifting, not naming guardians).
	Access    string                 `json:"access"`
	Guardians []parentalpkg.Guardian `json:"guardians"`
	Signals   []parentalpkg.Signal   `json:"signals"`
}

func accessName(a parentalpkg.Access) string {
	if a == parentalpkg.AccessAdmin {
		return "admin"
	}
	return "guardian"
}

func (h *Handler) writePolicy(w http.ResponseWriter, r *http.Request, id string, access parentalpkg.Access, pol *parentalpkg.Policy) {
	ctx := r.Context()
	var err error
	if pol == nil {
		if pol, err = h.Store.Policy(ctx, id); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	guardians, err := h.Store.Guardians(ctx, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	signals, err := h.Store.Signals(ctx, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, policyResponse{Policy: pol, Access: accessName(access), Guardians: guardians, Signals: signals})
}

// Get implements GET /api/users/{id}/parental.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	access, ok := h.reader(w, r, id)
	if !ok {
		return
	}
	h.writePolicy(w, r, id, access, nil)
}

type putRequest struct {
	MaxTier   *int   `json:"max_tier"`
	Unrated   string `json:"unrated"`
	AllowOnly bool   `json:"allow_only"`
	Version   int    `json:"version"`
}

func (req putRequest) input() (parentalpkg.PolicyInput, *httperror.Error) {
	var errs []httperror.FieldError
	if req.MaxTier == nil || !parentalpkg.ValidPolicyTier(*req.MaxTier) {
		errs = append(errs, httperror.FieldError{Field: "max_tier", Message: "must be one of -1, 0, 9, 13, 16, 18"})
	}
	unrated := req.Unrated
	if unrated == "" {
		unrated = parentalpkg.UnratedHide
	}
	if !parentalpkg.ValidUnrated(unrated) {
		errs = append(errs, httperror.FieldError{Field: "unrated", Message: "must be hide or one of tier_0, tier_9, tier_13, tier_16, tier_18"})
	}
	if req.Version < 0 {
		errs = append(errs, httperror.FieldError{Field: "version", Message: "must not be negative"})
	}
	if len(errs) > 0 {
		return parentalpkg.PolicyInput{}, httperror.Unprocessable(errs)
	}
	return parentalpkg.PolicyInput{
		MaxTier: parentalpkg.PolicyTier(*req.MaxTier), Unrated: unrated, AllowOnly: req.AllowOnly, Version: req.Version,
	}, nil
}

// Put implements PUT /api/users/{id}/parental: an admin makes the account
// restricted (version 0 or left out) or changes it; a guardian changes it.
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.manager(w, r, id)
	if !ok {
		return
	}
	var req putRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	in, e := req.input()
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	pol, err := h.Store.SetPolicy(r.Context(), p.UserID, id, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.changed(id)
	access := parentalpkg.AccessGuardian
	if p.IsAdmin {
		access = parentalpkg.AccessAdmin
	}
	h.writePolicy(w, r, id, access, pol)
}

// changed drops this process's cached policy at once (the other processes hear
// the notification).
func (h *Handler) changed(id string) {
	if h.Cache != nil {
		h.Cache.Invalidate(id)
	}
}

// Lift implements DELETE /api/users/{id}/parental (admin).
func (h *Handler) Lift(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.admin(w, r, id, true)
	if !ok {
		return
	}
	if err := h.Store.Lift(r.Context(), p.UserID, id); err != nil {
		h.fail(w, r, err)
		return
	}
	h.changed(id)
	common.WriteNoContent(w)
}

type guardianRequest struct {
	GuardianID string `json:"guardian_id"`
}

type guardiansResponse struct {
	Guardians []parentalpkg.Guardian `json:"guardians"`
}

// AddGuardian implements POST /api/users/{id}/parental/guardians (admin).
func (h *Handler) AddGuardian(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.admin(w, r, id, false)
	if !ok {
		return
	}
	var req guardianRequest
	if e := common.ReadJSON(r, &req, 1<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.GuardianID == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "guardian_id", Message: "must not be empty"}}))
		return
	}
	if err := h.Store.AddGuardian(r.Context(), p.UserID, id, req.GuardianID); err != nil {
		if errors.Is(err, parentalpkg.ErrUserNotFound) {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "guardian_id", Message: "no such user"}}))
			return
		}
		h.fail(w, r, err)
		return
	}
	h.changed(id)
	list, err := h.Store.Guardians(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, guardiansResponse{Guardians: list})
}

// RemoveGuardian implements DELETE /api/users/{id}/parental/guardians/{gid}
// (admin). Admins still look after the child, so no child is left unmanaged.
func (h *Handler) RemoveGuardian(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.admin(w, r, id, false)
	if !ok {
		return
	}
	if err := h.Store.RemoveGuardian(r.Context(), p.UserID, id, chi.URLParam(r, "gid")); err != nil {
		h.fail(w, r, err)
		return
	}
	h.changed(id)
	common.WriteNoContent(w)
}

type rulesResponse struct {
	Rules []parentalpkg.Rule `json:"rules"`
}

// Rules implements GET /api/users/{id}/parental/rules.
func (h *Handler) Rules(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.reader(w, r, id); !ok {
		return
	}
	if _, err := h.Store.Policy(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	list, err := h.Store.Rules(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, rulesResponse{Rules: list})
}

type ruleRequest struct {
	Effect      string     `json:"effect"`
	SubjectKind string     `json:"subject_kind"`
	SubjectID   string     `json:"subject_id"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

// AddRule implements POST /api/users/{id}/parental/rules.
func (h *Handler) AddRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.manager(w, r, id)
	if !ok {
		return
	}
	var req ruleRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	var errs []httperror.FieldError
	if req.Effect != parentalpkg.EffectAllow && req.Effect != parentalpkg.EffectBlock {
		errs = append(errs, httperror.FieldError{Field: "effect", Message: "must be allow or block"})
	}
	if !parentalpkg.ValidSubjectKind(req.SubjectKind) {
		errs = append(errs, httperror.FieldError{Field: "subject_kind",
			Message: "must be one of library, collection, series, channel, youtube_source, plugin, video"})
	}
	if req.SubjectID == "" || len(req.SubjectID) > 200 {
		errs = append(errs, httperror.FieldError{Field: "subject_id", Message: "must be 1 to 200 characters"})
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		errs = append(errs, httperror.FieldError{Field: "expires_at", Message: "must be in the future"})
	}
	if len(errs) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(errs))
		return
	}
	rule, err := h.Store.AddRule(r.Context(), p.UserID, id, parentalpkg.RuleInput{
		Effect: req.Effect, SubjectKind: req.SubjectKind, SubjectID: req.SubjectID, ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		if errors.Is(err, parentalpkg.ErrInvalid) {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "subject_id", Message: "not a valid id"}}))
			return
		}
		h.fail(w, r, err)
		return
	}
	h.changed(id)
	common.WriteJSON(w, r, http.StatusCreated, rule)
}

// RemoveRule implements DELETE /api/users/{id}/parental/rules/{rid}.
func (h *Handler) RemoveRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.manager(w, r, id)
	if !ok {
		return
	}
	if err := h.Store.RemoveRule(r.Context(), p.UserID, id, chi.URLParam(r, "rid")); err != nil {
		h.fail(w, r, err)
		return
	}
	h.changed(id)
	common.WriteNoContent(w)
}

// meResponse is the caller's own limit: no rules and no names, for the UI.
type meResponse struct {
	Restricted bool   `json:"restricted"`
	MaxTier    *int   `json:"max_tier,omitempty"`
	Unrated    string `json:"unrated,omitempty"`
	AllowOnly  bool   `json:"allow_only,omitempty"`
	Version    int    `json:"version,omitempty"`
}

// Me implements GET /api/me/parental: the limit this request is held to.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	p := h.caller(w, r)
	if p == nil {
		return
	}
	var (
		e   *parentalpkg.Effective
		err error
	)
	if h.Cache != nil {
		e, err = h.Cache.For(r.Context(), p)
	} else {
		e, err = h.Store.Effective(r.Context(), p.UserID)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := meResponse{Restricted: e.Restricted}
	if e.Restricted {
		tier := int(e.MaxTier)
		out.MaxTier, out.Unrated, out.AllowOnly, out.Version = &tier, e.Unrated, e.AllowOnly, e.Version
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}
