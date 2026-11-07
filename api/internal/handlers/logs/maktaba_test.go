package logs

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// switches is reports.share's state.
type switches struct{ on bool }

func (s *switches) On(id string) bool { return id == ShareComponent && s.on }

// fakeAgent is the home's relay agent and the cloud behind it.
type fakeAgent struct {
	mu      sync.Mutex
	down    bool
	posts   []map[string]any
	shots   int
	deletes int
	replies string
	srv     *httptest.Server
}

func newAgent(t *testing.T) *fakeAgent {
	a := &fakeAgent{replies: `{"replies":[]}`}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.down {
			http.Error(w, "the cloud is out of reach", http.StatusBadGateway)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/admin/cloud-link/reports":
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(p)
				switch p.FormName() {
				case "report":
					var v map[string]any
					_ = json.Unmarshal(b, &v)
					a.posts = append(a.posts, v)
				case "screenshot":
					a.shots++
				}
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":`+itoa(100+len(a.posts))+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/admin/cloud-link/reports/replies":
			_, _ = io.WriteString(w, a.replies)
		case r.Method == http.MethodDelete && r.URL.Path == "/admin/cloud-link/reports":
			a.deletes++
			_, _ = io.WriteString(w, `{"deleted":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (a *fakeAgent) set(down bool) { a.mu.Lock(); a.down = down; a.mu.Unlock() }

type shareRig struct {
	t     *testing.T
	h     *Handler
	r     http.Handler
	sw    *switches
	agent *fakeAgent
	now   time.Time
}

func newShareRig(t *testing.T) *shareRig {
	h, r := reportsRouter(t, "")
	rg := &shareRig{t: t, h: h, r: r, sw: &switches{}, agent: newAgent(t), now: time.Now().UTC()}
	h.Switches = rg.sw
	h.AgentAddr = strings.TrimPrefix(rg.agent.srv.URL, "http://")
	h.Forwarder().now = func() time.Time { return rg.now }
	return rg
}

var (
	sara  = &principal.Principal{UserID: "u-sara"}
	admin = &principal.Principal{UserID: "u-admin", IsAdmin: true}
)

func (rg *shareRig) do(method, path string, body io.Reader, ct string, p *principal.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	rg.r.ServeHTTP(rec, as(p, req))
	return rec
}

func (rg *shareRig) send(share bool, shot []byte) *httptest.ResponseRecorder {
	rg.t.Helper()
	rep := `{"category":"playback","text":"Sara here: it stopped","app":"web","version":"0.3.95","platform":"web",` +
		`"install_id":"b1","screenshot_previewed":true,"share_with_maktaba":` + map[bool]string{true: "true", false: "false"}[share] +
		`,"lines":[{"msg":"app_line","title":"The Seerah","user_h":"abcdef0123456789"}]}`
	body, ct := reportForm(rg.t, rep, shot)
	return rg.do("POST", "/api/reports", body, ct, sara)
}

func (rg *shareRig) report(id int) Report {
	rg.t.Helper()
	rep, _, err := readReport(rg.h.reportsDir(), id)
	if err != nil {
		rg.t.Fatal(err)
	}
	return rep
}

// T02: the box goes to Maktaba only with the setting on and the box ticked.
func TestAReportGoesToMaktabaOnlyWithTheSettingOnAndTheBoxTicked(t *testing.T) {
	rg := newShareRig(t)
	if rec := rg.do("GET", "/api/reports/maktaba", nil, "", sara); !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("off: the apps show no box: %s", rec.Body)
	}
	// off: a ticked box is refused, naming the component
	rec := rg.send(true, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"component":"reports.share"`) ||
		!strings.Contains(rec.Body.String(), `"code":"component_off"`) {
		t.Fatalf("off and ticked: %d %s", rec.Code, rec.Body)
	}
	if rec := rg.send(false, nil); rec.Code != http.StatusCreated {
		t.Fatalf("off, unticked: %d", rec.Code)
	}
	rg.sw.on = true
	if rec := rg.do("GET", "/api/reports/maktaba", nil, "", sara); !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("on: the box shows: %s", rec.Body)
	}
	if rec := rg.send(false, nil); rec.Code != http.StatusCreated {
		t.Fatalf("on, unticked: %d", rec.Code)
	}
	if rec := rg.send(true, []byte("png")); rec.Code != http.StatusCreated {
		t.Fatalf("on, ticked: %d %s", rec.Code, rec.Body)
	}
	// the sender's name, as the home reads it from its users (no database here)
	if _, err := updateReport(rg.h.reportsDir(), 3, func(x *Report) error { x.Name = "Sara"; return nil }); err != nil {
		t.Fatal(err)
	}
	rg.h.Forwarder().Pass(context.Background())
	if len(rg.agent.posts) != 1 || rg.agent.shots != 1 {
		t.Fatalf("sent: %d reports, %d screenshots", len(rg.agent.posts), rg.agent.shots)
	}
	got, _ := json.Marshal(rg.agent.posts[0])
	for _, gone := range []string{"Sara", "u-sara", "The Seerah", `"title"`, "abcdef0123456789", `"name"`} {
		if strings.Contains(string(got), gone) {
			t.Errorf("%q left the home: %s", gone, got)
		}
	}
	if rg.agent.posts[0]["id"] != float64(3) || !strings.Contains(string(got), `"user_h":"user-1"`) {
		t.Errorf("the report as it left: %s", got)
	}
	if m := rg.report(3).Maktaba; m == nil || m.State != ShareSent || m.CloudID != 101 {
		t.Fatalf("its state: %+v", m)
	}
	for _, id := range []int{1, 2} {
		if rg.report(id).Maktaba != nil {
			t.Errorf("report %d, unticked, has a way to Maktaba", id)
		}
	}
	// its sender sees it went; a pass more sends nothing again
	if rec := rg.do("GET", "/api/reports/3", nil, "", sara); !strings.Contains(rec.Body.String(), `"state":"sent"`) {
		t.Errorf("the sender: %s", rec.Body)
	}
	rg.h.Forwarder().Pass(context.Background())
	if len(rg.agent.posts) != 1 {
		t.Errorf("sent again: %d", len(rg.agent.posts))
	}
}

// T03: while the cloud is down the report waits, backing off; it arrives once when the link
// is back; after 7 days it is not sent.
func TestAReportWaitsForTheCloudAndArrivesOnce(t *testing.T) {
	rg := newShareRig(t)
	rg.sw.on = true
	rg.agent.set(true)
	rg.send(true, nil)
	f := rg.h.Forwarder()
	f.Pass(context.Background())
	m := rg.report(1).Maktaba
	if m.State != ShareQueued || m.Attempts != 1 || !m.NextAt.Equal(rg.now.Add(time.Minute)) {
		t.Fatalf("after the first try: %+v", m)
	}
	rg.now = rg.now.Add(time.Minute)
	f.Pass(context.Background())
	if m := rg.report(1).Maktaba; m.Attempts != 2 || !m.NextAt.Equal(rg.now.Add(2*time.Minute)) {
		t.Fatalf("backing off: %+v", m)
	}
	// not due yet: not tried
	f.Pass(context.Background())
	if m := rg.report(1).Maktaba; m.Attempts != 2 {
		t.Fatalf("tried before its time: %+v", m)
	}
	rg.agent.set(false)
	rg.now = rg.now.Add(2 * time.Minute)
	f.Pass(context.Background())
	f.Pass(context.Background())
	if len(rg.agent.posts) != 1 || rg.report(1).Maktaba.State != ShareSent {
		t.Fatalf("delivered once: %d, %+v", len(rg.agent.posts), rg.report(1).Maktaba)
	}
	// a report that waited past 7 days is not sent
	rg.agent.set(true)
	rg.send(true, nil)
	rg.now = rg.now.Add(ShareKeepFor + time.Hour)
	f.Pass(context.Background())
	if m := rg.report(2).Maktaba; m.State != ShareExpired {
		t.Fatalf("after 7 days: %+v", m)
	}
}

// Maktaba's reply reaches the report: its sender and the admins see it.
func TestMaktabasReplyReachesTheReport(t *testing.T) {
	rg := newShareRig(t)
	rg.sw.on = true
	rg.send(true, nil)
	f := rg.h.Forwarder()
	f.Pass(context.Background())
	at := rg.now.Add(time.Hour).Format(time.RFC3339Nano)
	rg.agent.replies = `{"replies":[{"home_report_id":1,"reply":"Fixed in 0.3.96, thank you","replied_at":"` + at +
		`","status":"fixed","fixed_in":"0.3.96"}]}`
	rg.now = rg.now.Add(repliesEvery)
	f.Pass(context.Background())
	rec := rg.do("GET", "/api/reports/1", nil, "", sara)
	if !strings.Contains(rec.Body.String(), `"reply":"Fixed in 0.3.96, thank you"`) || !strings.Contains(rec.Body.String(), `"fixed_in":"0.3.96"`) {
		t.Fatalf("the sender: %s", rec.Body)
	}
	if rec := rg.do("GET", "/api/reports", nil, "", admin); !strings.Contains(rec.Body.String(), "Fixed in 0.3.96") {
		t.Fatalf("the admins' inbox: %s", rec.Body)
	}
	if st := rg.h.readState(); st.RepliesSince.Format(time.RFC3339Nano) != at {
		t.Fatalf("read up to %v", st.RepliesSince)
	}
}

// An admin sends a report later, with their own preview, and may drop its screenshot; off,
// the route answers component_off; turning the setting off stops what waits.
func TestAnAdminSendsAReportWithTheirPreview(t *testing.T) {
	rg := newShareRig(t)
	rg.send(false, []byte("png"))
	if rec := rg.do("POST", "/api/reports/1/share", strings.NewReader(`{"screenshot":true}`), "application/json", admin); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), `"component":"reports.share"`) {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}
	rg.sw.on = true
	if rec := rg.do("POST", "/api/reports/1/share", nil, "", sara); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer: %d", rec.Code)
	}
	if rec := rg.do("GET", "/api/reports/1/screenshot", nil, "", admin); rec.Code != 200 || rec.Body.String() != "png" {
		t.Fatalf("the admin sees the screenshot first: %d", rec.Code)
	}
	if rec := rg.do("GET", "/api/reports/1/screenshot", nil, "", sara); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer reads no screenshot here: %d", rec.Code)
	}
	prev := rg.do("GET", "/api/reports/1/maktaba-preview", nil, "", admin)
	if prev.Code != 200 || strings.Contains(prev.Body.String(), "Seerah") || !strings.Contains(prev.Body.String(), `"user_h":"user-1"`) {
		t.Fatalf("the preview: %d %s", prev.Code, prev.Body)
	}
	if rec := rg.do("POST", "/api/reports/1/share", strings.NewReader(`{"screenshot":false}`), "application/json", admin); rec.Code != http.StatusAccepted {
		t.Fatalf("share: %d %s", rec.Code, rec.Body)
	}
	if rec := rg.do("POST", "/api/reports/1/share", nil, "", admin); rec.Code != http.StatusConflict {
		t.Fatalf("again: %d", rec.Code)
	}
	rg.h.Forwarder().Pass(context.Background())
	if len(rg.agent.posts) != 1 || rg.agent.shots != 0 {
		t.Fatalf("sent %d, screenshots %d (dropped)", len(rg.agent.posts), rg.agent.shots)
	}
	// turning the setting off: what waits is not sent later
	rg.agent.set(true)
	rg.send(true, nil)
	rg.h.cancelQueued()
	rg.agent.set(false)
	rg.h.Forwarder().Pass(context.Background())
	if m := rg.report(2).Maktaba; m.State != ShareCancelled || len(rg.agent.posts) != 1 {
		t.Fatalf("after off: %+v, %d sent", m, len(rg.agent.posts))
	}
}

// "Delete what Maktaba has": at once when the cloud answers, else when it does.
func TestDeleteWhatMaktabaHas(t *testing.T) {
	rg := newShareRig(t)
	rg.sw.on = true
	rg.send(true, nil)
	rg.h.Forwarder().Pass(context.Background())
	rg.agent.set(true)
	rec := rg.do("DELETE", "/api/reports/maktaba", nil, "", admin)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"pending"`) {
		t.Fatalf("cloud down: %d %s", rec.Code, rec.Body)
	}
	if rec := rg.do("GET", "/api/reports/maktaba", nil, "", admin); !strings.Contains(rec.Body.String(), `"state":"pending"`) {
		t.Fatalf("pending: %s", rec.Body)
	}
	rg.agent.set(false)
	// the admin's next look passes it
	if rec := rg.do("GET", "/api/reports/maktaba", nil, "", admin); !strings.Contains(rec.Body.String(), `"state":"done"`) {
		t.Fatalf("done: %s", rec.Body)
	}
	if rg.agent.deletes != 1 || rg.report(1).Maktaba.State != ShareDeleted {
		t.Fatalf("deleted %d times, %+v", rg.agent.deletes, rg.report(1).Maktaba)
	}
	if rec := rg.do("DELETE", "/api/reports/maktaba", nil, "", sara); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer: %d", rec.Code)
	}
}

// Not linked to Maktaba Cloud: no box, and the setting cannot be turned on.
func TestWithoutTheCloudLinkThereIsNoBox(t *testing.T) {
	rg := newShareRig(t)
	rg.h.AgentAddr = ""
	t.Setenv("MAKTABA_CLOUDLINK_ADMIN", "")
	rg.sw.on = true
	if rec := rg.do("GET", "/api/reports/maktaba", nil, "", sara); !strings.Contains(rec.Body.String(), `"available":false,"enabled":false`) {
		t.Fatalf("%s", rec.Body)
	}
	if rec := rg.do("PUT", "/api/reports/maktaba", strings.NewReader(`{"enabled":true}`), "application/json", admin); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "cloud_not_linked") {
		t.Fatalf("turning it on: %d %s", rec.Code, rec.Body)
	}
	if rec := rg.send(true, nil); rec.Code != http.StatusConflict {
		t.Fatalf("a ticked box: %d", rec.Code)
	}
}

func TestParseRoots(t *testing.T) {
	if got := strings.Join(parseRoots(`{/media/a,"/media/b c"}`), "|"); got != "/media/a|/media/b c" {
		t.Fatalf("%q", got)
	}
	if got := strings.Join(parseRoots(`["/x","/y z"]`), "|"); got != "/x|/y z" {
		t.Fatalf("%q", got)
	}
}
