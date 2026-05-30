package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
)

// fakeUserAdmin is the in-memory stand-in for *users.Store used to
// exercise the admin user-management HTTP surface without a DB.
type fakeUserAdmin struct {
	created   *users.CreateInput
	createErr error
	updated   *users.UpdateInput
	updateID  string
	updateErr error
	deletedID string
	deleteErr error
	unlockID  string
	unlockErr error
}

func (f *fakeUserAdmin) Create(_ context.Context, in users.CreateInput) (*users.User, error) {
	f.created = &in
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &users.User{ID: "new-id", Username: in.Username, IsAdmin: in.IsAdmin}, nil
}

func (f *fakeUserAdmin) Update(_ context.Context, id string, in users.UpdateInput) (*users.User, error) {
	f.updateID, f.updated = id, &in
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &users.User{ID: id, Username: "updated"}, nil
}

func (f *fakeUserAdmin) Delete(_ context.Context, id string) error {
	f.deletedID = id
	return f.deleteErr
}

func (f *fakeUserAdmin) Unlock(_ context.Context, id string) error {
	f.unlockID = id
	return f.unlockErr
}

func adminReq(method, target, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	return r.WithContext(principal.WithPrincipal(r.Context(), &principal.Principal{
		UserID: "admin-1", IsAdmin: true, Source: principal.SourceJWT,
	}))
}

func withChiParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// E10-USR-1: non-admin principal is 403 on every user-mgmt verb.
func TestUsersAdmin_NonAdminForbidden(t *testing.T) {
	h := &Handler{UserAdmin: &fakeUserAdmin{}}
	r := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{}`))
	r = r.WithContext(principal.WithPrincipal(r.Context(), &principal.Principal{
		UserID: "u1", IsAdmin: false, Source: principal.SourceJWT,
	}))
	rec := httptest.NewRecorder()
	h.AdminCreateUser(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin create: status = %d, want 403", rec.Code)
	}
}

// E10-USR-2: anonymous (no principal) is 401.
func TestUsersAdmin_AnonymousUnauthorized(t *testing.T) {
	h := &Handler{UserAdmin: &fakeUserAdmin{}}
	rec := httptest.NewRecorder()
	h.AdminCreateUser(rec, httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous create: status = %d, want 401", rec.Code)
	}
}

// E10-USR-3: create happy path → 201 with the user projection (no hash).
func TestUsersAdmin_CreateOK(t *testing.T) {
	fa := &fakeUserAdmin{}
	h := &Handler{UserAdmin: fa}
	rec := httptest.NewRecorder()
	h.AdminCreateUser(rec, adminReq(http.MethodPost, "/api/users",
		`{"username":"bob","password":"pw-correct-horse","is_admin":true}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fa.created == nil || fa.created.Username != "bob" || !fa.created.IsAdmin {
		t.Fatalf("create input = %+v", fa.created)
	}
	if strings.Contains(rec.Body.String(), "pw_hash") || strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("response leaked secret material: %s", rec.Body.String())
	}
}

// E10-USR-4: username conflict from the store → 409 username-exists.
func TestUsersAdmin_CreateUsernameConflict409(t *testing.T) {
	h := &Handler{UserAdmin: &fakeUserAdmin{createErr: users.ErrUsernameExists}}
	rec := httptest.NewRecorder()
	h.AdminCreateUser(rec, adminReq(http.MethodPost, "/api/users",
		`{"username":"dup","password":"pw-correct-horse"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "username-exists") {
		t.Fatalf("body = %s, want username-exists type", rec.Body.String())
	}
}

// E10-USR-5: PATCH partial update → 200 and forwards the parsed input.
func TestUsersAdmin_UpdateOK(t *testing.T) {
	fa := &fakeUserAdmin{}
	h := &Handler{UserAdmin: fa}
	r := withChiParam(adminReq(http.MethodPatch, "/api/users/u9", `{"is_admin":false}`), "id", "u9")
	rec := httptest.NewRecorder()
	h.AdminUpdateUser(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fa.updateID != "u9" || fa.updated == nil || fa.updated.IsAdmin == nil || *fa.updated.IsAdmin {
		t.Fatalf("update id=%q in=%+v", fa.updateID, fa.updated)
	}
}

// E10-USR-6: demoting the last admin → 409 last-admin.
func TestUsersAdmin_UpdateLastAdmin409(t *testing.T) {
	h := &Handler{UserAdmin: &fakeUserAdmin{updateErr: users.ErrLastAdmin}}
	r := withChiParam(adminReq(http.MethodPatch, "/api/users/u9", `{"is_admin":false}`), "id", "u9")
	rec := httptest.NewRecorder()
	h.AdminUpdateUser(rec, r)
	if rec.Code != http.StatusConflict || !contains(rec.Body.String(), "last-admin") {
		t.Fatalf("status = %d body=%s, want 409 last-admin", rec.Code, rec.Body.String())
	}
}

// E10-USR-7: DELETE happy path → 204.
func TestUsersAdmin_DeleteOK(t *testing.T) {
	fa := &fakeUserAdmin{}
	h := &Handler{UserAdmin: fa}
	r := withChiParam(adminReq(http.MethodDelete, "/api/users/u3", ""), "id", "u3")
	rec := httptest.NewRecorder()
	h.AdminDeleteUser(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if fa.deletedID != "u3" {
		t.Fatalf("deleted id = %q, want u3", fa.deletedID)
	}
}

// E10-USR-8: DELETE unknown id → 404.
func TestUsersAdmin_DeleteNotFound404(t *testing.T) {
	h := &Handler{UserAdmin: &fakeUserAdmin{deleteErr: users.ErrNotFound}}
	r := withChiParam(adminReq(http.MethodDelete, "/api/users/missing", ""), "id", "missing")
	rec := httptest.NewRecorder()
	h.AdminDeleteUser(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// E10-USR-9: unlock happy path → 204 and clears the brute-force lock
// (the operator escape hatch for a locked account — HLB-391/398).
func TestUsersAdmin_UnlockOK(t *testing.T) {
	fa := &fakeUserAdmin{}
	h := &Handler{UserAdmin: fa}
	r := withChiParam(adminReq(http.MethodPost, "/api/users/u7/unlock", ""), "id", "u7")
	rec := httptest.NewRecorder()
	h.AdminUnlockUser(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if fa.unlockID != "u7" {
		t.Fatalf("unlock id = %q, want u7", fa.unlockID)
	}
}

// E10-USR-10: *users.Store satisfies the UserAdmin seam, so the live
// route drives the real store CRUD.
func TestUsersStore_SatisfiesUserAdminSeam(_ *testing.T) {
	var _ UserAdmin = (*users.Store)(nil)
}

// E10-USR-11: empty password is rejected 422 before touching the store
// (defence in depth; the store would hash an empty string otherwise).
func TestUsersAdmin_CreateRejectsEmptyPassword(t *testing.T) {
	fa := &fakeUserAdmin{}
	h := &Handler{UserAdmin: fa}
	rec := httptest.NewRecorder()
	h.AdminCreateUser(rec, adminReq(http.MethodPost, "/api/users", `{"username":"bob","password":""}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if fa.created != nil {
		t.Fatal("store must not be called when password is empty")
	}
}
