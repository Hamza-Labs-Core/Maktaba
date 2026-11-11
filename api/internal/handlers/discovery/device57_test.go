package discovery

// Epic 57 story 57.02 (T06): a guardian or an admin approves a device as a
// restricted account they look after; no one else can be named.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// approveAsFake stands for parental.Store.ApproveAs: "guardian-1" looks after
// "child-1"; "child-1" is restricted.
func approveAsFake(_ context.Context, p *principal.Principal, asUser string) (string, error) {
	switch {
	case p.UserID == "child-1":
		return "", ErrApproveAsRestricted
	case asUser == "child-1" && (p.UserID == "guardian-1" || p.IsAdmin):
		return asUser, nil
	case asUser == "broken":
		return "", errors.New("database is away")
	}
	return "", ErrApproveAsNotFound
}

func TestDeviceApprovedAsTheChild(t *testing.T) {
	h, fm, r, _ := newDeviceHandler(t)
	h.ApproveAs = approveAsFake
	s := startWith(t, r, "player/1.0.0 (tvos; tv)", `{"device_label":"Playroom TV"}`)
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`","as_user":"child-1"}`, "guardian-1"); rec.Code != http.StatusOK {
		t.Fatalf("a guardian approving as their child = %d %s", rec.Code, rec.Body.String())
	}
	if rec := poll(r, s.DeviceCode); rec.Code != http.StatusOK {
		t.Fatalf("collect = %d %s", rec.Code, rec.Body.String())
	}
	if fm.lastUID != "child-1" {
		t.Fatalf("the device was signed in as %q, want the child", fm.lastUID)
	}
}

func TestDeviceApproveAsRefusals(t *testing.T) {
	cases := []struct {
		name, caller, as string
		want             int
		code             string
	}{
		{"a guardian as another's child", "guardian-2", "child-1", http.StatusNotFound, ""},
		{"a guardian as an unrestricted user", "guardian-1", "viewer-1", http.StatusNotFound, ""},
		{"a viewer as a child", "viewer-1", "child-1", http.StatusNotFound, ""},
		{"a child as its guardian", "child-1", "guardian-1", http.StatusForbidden, "restricted"},
		{"the store failing", "guardian-1", "broken", http.StatusInternalServerError, ""},
	}
	for _, c := range cases {
		h, fm, r, _ := newDeviceHandler(t)
		h.ApproveAs = approveAsFake
		s := startDevice(t, r)
		rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`","as_user":"`+c.as+`"}`, c.caller)
		if rec.Code != c.want {
			t.Errorf("%s = %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
			continue
		}
		if c.code != "" {
			var body map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["code"] != c.code {
				t.Errorf("%s: code = %v, want %s", c.name, body["code"], c.code)
			}
		}
		// a refused approval leaves the code unapproved: the app still waits
		if rec := poll(r, s.DeviceCode); rec.Code == http.StatusOK || fm.calls != 0 {
			t.Errorf("%s: the refused approval signed the device in (%d)", c.name, rec.Code)
		}
	}
}

// Naming oneself, or no one, is the caller's own sign-in and asks no one; a child
// approves a device for itself this way.
func TestDeviceApproveAsOneself(t *testing.T) {
	for _, body := range []string{`"as_user":"child-1"`, `"as_user":""`} {
		h, fm, r, _ := newDeviceHandler(t)
		h.ApproveAs = func(context.Context, *principal.Principal, string) (string, error) {
			t.Fatal("asked who to sign in for the caller's own approval")
			return "", nil
		}
		s := startDevice(t, r)
		if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`",`+body+`}`, "child-1"); rec.Code != http.StatusOK {
			t.Fatalf("approve with %s = %d %s", body, rec.Code, rec.Body.String())
		}
		poll(r, s.DeviceCode)
		if fm.lastUID != "child-1" {
			t.Fatalf("signed in as %q, want the caller", fm.lastUID)
		}
	}
}

// With no parental store wired, no one else can be named.
func TestDeviceApproveAsWithoutAStore(t *testing.T) {
	_, fm, r, _ := newDeviceHandler(t)
	s := startDevice(t, r)
	if rec := do(r, http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`","as_user":"child-1"}`, "guardian-1"); rec.Code != http.StatusNotFound {
		t.Fatalf("approve as another with no store = %d, want 404", rec.Code)
	}
	if fm.calls != 0 {
		t.Fatal("minted")
	}
}

// A sign-in through Maktaba Cloud approves no device, as a child or otherwise.
func TestDeviceApproveAsFromACloudSession(t *testing.T) {
	h, _, r, _ := newDeviceHandler(t)
	h.ApproveAs = approveAsFake
	s := startDevice(t, r)
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/approve", strings.NewReader(`{"code":"`+s.Code+`","as_user":"child-1"}`))
	req = req.WithContext(principal.WithPrincipal(context.Background(),
		&principal.Principal{UserID: "guardian-1", CloudScope: principal.CloudScopeManage}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("approve as a child from a cloud session = %d, want 403", rec.Code)
	}
}
