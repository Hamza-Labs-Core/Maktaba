package discovery

// Epic 58 story 58.03: the home's side of a TV's cloud code.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// fakeCloud is CloudCodes: one open code, a claim that fails when claimErr is set.
type fakeCloud struct {
	open     string
	claimErr error
	grants   []string
}

func (f *fakeCloud) Lookup(_ context.Context, code string) (CloudCode, error) {
	if code != f.open {
		return CloudCode{}, ErrCloudCodeNotFound
	}
	return CloudCode{Code: code, Platform: "webos", Country: "AE"}, nil
}

func (f *fakeCloud) Claim(_ context.Context, code, grant string) (CloudCode, error) {
	if f.claimErr != nil {
		return CloudCode{}, f.claimErr
	}
	f.grants = append(f.grants, grant)
	f.open = ""
	return CloudCode{Code: code, Platform: "webos", Country: "AE"}, nil
}

func newCloudHandler(t *testing.T, cloud CloudCodes) (*Handler, *fakeMinter, http.Handler) {
	t.Helper()
	h, fm, _, _ := newDeviceHandler(t)
	if cloud != nil {
		h.CloudCodes = cloud
	}
	r := chi.NewRouter()
	h.Mount(r)
	h.MountDevice(r, -1)
	h.MountCloudCode(r)
	return h, fm, r
}

func TestCloudApproveThenTheTVRedeems(t *testing.T) {
	cloud := &fakeCloud{open: "WEBS2345"}
	_, fm, r := newCloudHandler(t, cloud)
	if rec := do(r, http.MethodGet, "/api/pairing/cloud-code/webs-2345", "", "viewer-1"); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"platform":"webos"`) {
		t.Fatalf("lookup = %d %s", rec.Code, rec.Body.String())
	}
	rec := do(r, http.MethodPost, "/api/pairing/cloud-approve", `{"code":"WEBS-2345"}`, "viewer-1")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"country":"AE"`) {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body.String())
	}
	// approved already: a phone is not told a TV is waiting
	if rec := do(r, http.MethodGet, "/api/pairing/pending", "", "viewer-1"); !strings.Contains(rec.Body.String(), `"count":0`) {
		t.Fatalf("pending = %s", rec.Body.String())
	}
	if len(cloud.grants) != 1 {
		t.Fatalf("grants = %v", cloud.grants)
	}
	if rec := poll(r, cloud.grants[0]); rec.Code != http.StatusOK {
		t.Fatalf("redeem = %d %s", rec.Code, rec.Body.String())
	}
	if fm.lastUID != "viewer-1" || fm.lastKnd != "tv" || fm.lastVia != ViaCloudCode {
		t.Fatalf("minted for %q kind %q via %q", fm.lastUID, fm.lastKnd, fm.lastVia)
	}
	if rec := poll(r, cloud.grants[0]); rec.Code == http.StatusOK {
		t.Fatal("redeemed twice")
	}
}

// A claim the cloud refuses takes the approval back: the grant redeems nothing.
func TestCloudApproveRollsBackAFailedClaim(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{ErrCloudCodeClaimed, http.StatusConflict},
		{ErrCloudNotLinked, http.StatusConflict},
		{errors.New("timeout"), http.StatusBadGateway},
	} {
		cloud := &fakeCloud{open: "WEBS2345", claimErr: c.err}
		h, _, r := newCloudHandler(t, cloud)
		rec := do(r, http.MethodPost, "/api/pairing/cloud-approve", `{"code":"WEBS2345"}`, "viewer-1")
		if rec.Code != c.want {
			t.Fatalf("%v: %d %s", c.err, rec.Code, rec.Body.String())
		}
		if n := len(h.requests.pending(h.now())); n != 0 {
			t.Fatalf("%v: %d requests left", c.err, n)
		}
		h.requests.mu.Lock()
		left := len(h.requests.m)
		h.requests.mu.Unlock()
		if left != 0 {
			t.Fatalf("%v: %d device codes left", c.err, left)
		}
	}
}

func TestCloudCodeRefusals(t *testing.T) {
	cloud := &fakeCloud{open: "WEBS2345"}
	_, _, r := newCloudHandler(t, cloud)
	if rec := do(r, http.MethodGet, "/api/pairing/cloud-code/NXPE2345", "", "viewer-1"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown code = %d", rec.Code)
	}
	if rec := do(r, http.MethodPost, "/api/pairing/cloud-approve", `{"code":"NXPE2345"}`, "viewer-1"); rec.Code != http.StatusNotFound {
		t.Fatalf("approve unknown = %d", rec.Code)
	}
	if rec := do(r, http.MethodPost, "/api/pairing/cloud-approve", `{"code":"WEBS2345"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no one = %d", rec.Code)
	}
	// no cloud wired: remote access is off
	_, _, off := newCloudHandler(t, nil)
	rec := do(off, http.MethodGet, "/api/pairing/cloud-code/WEBS2345", "", "viewer-1")
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusConflict || body["code"] != "cloud_not_linked" {
		t.Fatalf("not linked = %d %s", rec.Code, rec.Body.String())
	}
}

// The agent client: what each answer of the relay agent means.
func TestAgentCloudCodesAnswers(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(`{"code":"WEBS-2345","platform":"roku","country":"US"}`))
	}))
	defer srv.Close()
	a := AgentCloudCodes{Addr: strings.TrimPrefix(srv.URL, "http://")}
	if c, err := a.Lookup(context.Background(), "WEBS2345"); err != nil || c.Platform != "roku" || c.Country != "US" {
		t.Fatalf("lookup = %+v %v", c, err)
	}
	for st, want := range map[int]error{http.StatusNotFound: ErrCloudCodeNotFound, http.StatusConflict: ErrCloudCodeClaimed,
		http.StatusUnauthorized: ErrCloudNotLinked} {
		status = st
		if _, err := a.Claim(context.Background(), "WEBS2345", "grant"); !errors.Is(err, want) {
			t.Fatalf("%d: %v", st, err)
		}
	}
	status = http.StatusBadGateway
	if _, err := a.Lookup(context.Background(), "WEBS2345"); err == nil || errors.Is(err, ErrCloudNotLinked) {
		t.Fatalf("502: %v", err)
	}
	// nothing listening: remote access is off
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_ = l.Close()
	if _, err := (AgentCloudCodes{Addr: addr}).Lookup(context.Background(), "WEBS2345"); !errors.Is(err, ErrCloudNotLinked) {
		t.Fatalf("no agent: %v", err)
	}
	t.Setenv("MAKTABA_CLOUDLINK_ADMIN", "")
	if _, err := (AgentCloudCodes{}).Lookup(context.Background(), "WEBS2345"); !errors.Is(err, ErrCloudNotLinked) {
		t.Fatalf("no address: %v", err)
	}
}

// The approval itself says it came by a cloud code: a home that restarted between the approval
// and the TV's redeem still mints a restricted sign-in.
func TestCloudApproveSurvivesARestart(t *testing.T) {
	cloud := &fakeCloud{open: "WEBS2345"}
	h, fm, r := newCloudHandler(t, cloud)
	if rec := do(r, http.MethodPost, "/api/pairing/cloud-approve", `{"code":"WEBS2345"}`, "viewer-1"); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body.String())
	}
	h.requests.mu.Lock()
	h.requests.m = map[string]deviceRequest{} // the restart: requests are in memory, tickets are not
	h.requests.mu.Unlock()
	if rec := poll(r, cloud.grants[0]); rec.Code != http.StatusOK {
		t.Fatalf("redeem after a restart = %d %s", rec.Code, rec.Body.String())
	}
	if fm.lastUID != "viewer-1" || fm.lastVia != ViaCloudCode {
		t.Fatalf("minted for %q via %q", fm.lastUID, fm.lastVia)
	}
}

// A restricted sign-in (a cloud code's TV) or a cloud one approves nothing: neither a cloud code
// nor a home code, and it is not told what is waiting.
func TestARestrictedSignInApprovesNothing(t *testing.T) {
	cloud := &fakeCloud{open: "WEBS2345"}
	_, _, r := newCloudHandler(t, cloud)
	s := startDevice(t, r)
	for _, p := range []*principal.Principal{{UserID: "admin-1", Restricted: true}, {UserID: "admin-1", CloudScope: principal.CloudScopeManage}} {
		as := func(method, path, body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req = req.WithContext(principal.WithPrincipal(context.Background(), p))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			return rec
		}
		if rec := as(http.MethodPost, "/api/pairing/cloud-approve", `{"code":"WEBS2345"}`); rec.Code != http.StatusForbidden {
			t.Fatalf("%+v cloud-approve = %d", p, rec.Code)
		}
		if rec := as(http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`); rec.Code != http.StatusForbidden {
			t.Fatalf("%+v approve = %d", p, rec.Code)
		}
		if rec := as(http.MethodGet, "/api/pairing/pending", ""); !strings.Contains(rec.Body.String(), `"count":0`) {
			t.Fatalf("%+v pending = %s", p, rec.Body.String())
		}
	}
	if len(cloud.grants) != 0 {
		t.Fatalf("grants = %v", cloud.grants)
	}
}
