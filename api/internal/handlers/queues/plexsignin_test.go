package queues

import (
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

// fakePlexTV is a minimal plex.tv: one PIN (id 42) that is authorised
// once authorised is set.
type fakePlexTV struct {
	mu         sync.Mutex
	authorised bool
	clientIDs  []string
	resToken   string
}

func (p *fakePlexTV) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if r.Header.Get("X-Plex-Product") != "Maktaba" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("missing plex headers on %s %s", r.Method, r.URL)
		}
		p.clientIDs = append(p.clientIDs, r.Header.Get("X-Plex-Client-Identifier"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v2/pins":
			if r.URL.Query().Get("strong") != "true" {
				t.Errorf("pin not strong: %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"id":42,"code":"ABCD"}`))
		case r.Method == "GET" && r.URL.Path == "/api/v2/pins/42":
			if p.authorised {
				_, _ = w.Write([]byte(`{"id":42,"code":"ABCD","authToken":"ACCOUNT-TOKEN"}`))
			} else {
				_, _ = w.Write([]byte(`{"id":42,"code":"ABCD","authToken":null}`))
			}
		case r.Method == "GET" && r.URL.Path == "/api/v2/resources":
			p.resToken = r.Header.Get("X-Plex-Token")
			_, _ = w.Write([]byte(`[
			 {"name":"Home","provides":"server","accessToken":"SERVER-TOKEN-1","connections":[
			   {"protocol":"https","uri":"https://relay.plex.direct:8443","local":false,"relay":true},
			   {"protocol":"https","uri":"https://remote.plex.direct:32400","local":false,"relay":false},
			   {"protocol":"http","uri":"http://192.168.1.5:32400","local":true,"relay":false},
			   {"protocol":"https","uri":"https://192-168-1-5.plex.direct:32400","local":true,"relay":false}]},
			 {"name":"Phone","provides":"client,player","accessToken":"X","connections":[
			   {"protocol":"http","uri":"http://10.0.0.9:1","local":true,"relay":false}]},
			 {"name":"Cabin","provides":"server,player","accessToken":"SERVER-TOKEN-2","connections":[
			   {"protocol":"http","uri":"http://cabin:32400","local":false,"relay":false}]}
			]`))
		default:
			http.NotFound(w, r)
		}
	})
}

func plexFixture(t *testing.T, settings string) (*fixture, *fakePlexTV) {
	t.Helper()
	fp := &fakePlexTV{}
	srv := httptest.NewServer(fp.handler(t))
	t.Cleanup(srv.Close)
	f := newFixture(&queuestest.Rule{Match: "from extensions", Cols: []string{"name", "enabled", "settings", "updated_at"},
		Rows: [][]driver.Value{{"plex", true, settings, t0}}})
	f.h.PlexTVURL = srv.URL
	f.h.PlexHTTP = srv.Client()
	return f, fp
}

func TestPlexSignin_RequireAdmin(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{"POST", "/api/extensions/plex/signin", ""},
		{"GET", "/api/extensions/plex/signin/42", ""},
		{"POST", "/api/extensions/plex/signin/42/use", `{"name":"Home"}`},
	}
	for _, rt := range routes {
		f, _ := plexFixture(t, `{}`)
		if rec := f.do(rt.method, rt.path, rt.body, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s anonymous: %d", rt.method, rt.path, rec.Code)
		}
		if rec := f.do(rt.method, rt.path, rt.body, viewer()); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s viewer: %d", rt.method, rt.path, rec.Code)
		}
	}
}

func TestPlexSignin_Create(t *testing.T) {
	t.Run("stores a new client id and returns the auth url", func(t *testing.T) {
		f, fp := plexFixture(t, `{"url":"http://old","path_map":"{}"}`)
		rec := f.do("POST", "/api/extensions/plex/signin", "", admin())
		if rec.Code != 200 {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		var out struct{ ID, Code, AuthURL string }
		var raw map[string]string
		decode(t, rec, &raw)
		out.ID, out.Code, out.AuthURL = raw["id"], raw["code"], raw["auth_url"]
		if out.ID != "42" || out.Code != "ABCD" {
			t.Fatalf("out = %v", raw)
		}
		ups := f.db.Find("insert into extensions")
		if len(ups) != 1 {
			t.Fatalf("client id not stored: %d writes", len(ups))
		}
		var saved map[string]any
		_ = json.Unmarshal([]byte(ups[0].Args[2].(string)), &saved)
		cid, _ := saved["client_id"].(string)
		if cid == "" || saved["url"] != "http://old" || saved["path_map"] != "{}" || ups[0].Args[1] != true {
			t.Fatalf("saved %v enabled %v", saved, ups[0].Args[1])
		}
		if fp.clientIDs[0] != cid {
			t.Errorf("plex.tv got client id %q, stored %q", fp.clientIDs[0], cid)
		}
		want := "https://app.plex.tv/auth#?clientID=" + cid + "&code=ABCD&context%5Bdevice%5D%5Bproduct%5D=Maktaba"
		if out.AuthURL != want {
			t.Errorf("auth_url = %s\nwant      %s", out.AuthURL, want)
		}
	})
	t.Run("reuses the stored client id", func(t *testing.T) {
		f, fp := plexFixture(t, `{"client_id":"maktaba-fixed"}`)
		rec := f.do("POST", "/api/extensions/plex/signin", "", admin())
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
		if len(f.db.Find("insert into extensions")) != 0 {
			t.Error("existing client id must not be rewritten")
		}
		if fp.clientIDs[0] != "maktaba-fixed" {
			t.Errorf("client id %q", fp.clientIDs[0])
		}
	})
	t.Run("plex.tv down is a 502", func(t *testing.T) {
		f, _ := plexFixture(t, `{"client_id":"c"}`)
		f.h.PlexTVURL = "http://127.0.0.1:1"
		if rec := f.do("POST", "/api/extensions/plex/signin", "", admin()); rec.Code != http.StatusBadGateway {
			t.Fatalf("status %d", rec.Code)
		}
	})
}

func TestPlexSignin_PollAndUse(t *testing.T) {
	f, fp := plexFixture(t, `{"client_id":"cid","path_map":"{\"/m\":\"E:/m\"}","url":"http://old","token":"OLD"}`)
	if rec := f.do("GET", "/api/extensions/plex/signin/99", "", admin()); rec.Code != 404 {
		t.Fatalf("unknown pin: %d", rec.Code)
	}
	if rec := f.do("POST", "/api/extensions/plex/signin", "", admin()); rec.Code != 200 {
		t.Fatalf("create: %d", rec.Code)
	}

	rec := f.do("GET", "/api/extensions/plex/signin/42", "", admin())
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"waiting"`) {
		t.Fatalf("waiting: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do("POST", "/api/extensions/plex/signin/42/use", `{"name":"Home"}`, admin()); rec.Code != http.StatusConflict {
		t.Fatalf("use before done: %d", rec.Code)
	}

	fp.mu.Lock()
	fp.authorised = true
	fp.mu.Unlock()
	rec = f.do("GET", "/api/extensions/plex/signin/42", "", admin())
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, "TOKEN") {
		t.Fatalf("done: %d %s", rec.Code, body)
	}
	if fp.resToken != "ACCOUNT-TOKEN" {
		t.Errorf("resources called with token %q", fp.resToken)
	}
	var out struct {
		State   string
		Servers []struct{ Name, URI string }
	}
	decode(t, rec, &out)
	if out.State != "done" || len(out.Servers) != 2 {
		t.Fatalf("out = %+v", out)
	}
	if out.Servers[0].Name != "Home" || out.Servers[0].URI != "https://192-168-1-5.plex.direct:32400" {
		t.Errorf("home = %+v", out.Servers[0])
	}
	if out.Servers[1].Name != "Cabin" || out.Servers[1].URI != "http://cabin:32400" {
		t.Errorf("cabin = %+v", out.Servers[1])
	}
	// A second poll answers from memory (no more plex.tv calls).
	n := len(fp.clientIDs)
	if rec := f.do("GET", "/api/extensions/plex/signin/42", "", admin()); rec.Code != 200 || len(fp.clientIDs) != n {
		t.Errorf("second poll: %d, calls %d -> %d", rec.Code, n, len(fp.clientIDs))
	}

	if rec := f.do("POST", "/api/extensions/plex/signin/42/use", `{"name":"Nope"}`, admin()); rec.Code != 400 {
		t.Fatalf("unknown server: %d", rec.Code)
	}
	rec = f.do("POST", "/api/extensions/plex/signin/42/use", `{"name":"Cabin"}`, admin())
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "TOKEN") {
		t.Fatalf("use: %d %s", rec.Code, rec.Body.String())
	}
	var ext Extension
	decode(t, rec, &ext)
	if ext.Name != "plex" || ext.Settings["url"] != "http://cabin:32400" || ext.Settings["token"] != SecretMask || !ext.Configured || !ext.Enabled {
		t.Errorf("ext = %+v", ext)
	}
	ups := f.db.Find("insert into extensions")
	var saved map[string]any
	_ = json.Unmarshal([]byte(ups[len(ups)-1].Args[2].(string)), &saved)
	if saved["url"] != "http://cabin:32400" || saved["token"] != "SERVER-TOKEN-2" || saved["path_map"] != `{"/m":"E:/m"}` || saved["client_id"] != "cid" {
		t.Errorf("saved %v", saved)
	}
	// The sign-in is consumed.
	if rec := f.do("GET", "/api/extensions/plex/signin/42", "", admin()); rec.Code != 404 {
		t.Errorf("after use: %d", rec.Code)
	}
}

func TestPlexSignin_Expires(t *testing.T) {
	f, _ := plexFixture(t, `{"client_id":"cid"}`)
	now := t0
	f.h.NowFunc = func() time.Time { return now }
	if rec := f.do("POST", "/api/extensions/plex/signin", "", admin()); rec.Code != 200 {
		t.Fatalf("create: %d", rec.Code)
	}
	now = t0.Add(16 * time.Minute)
	if rec := f.do("GET", "/api/extensions/plex/signin/42", "", admin()); rec.Code != 404 {
		t.Fatalf("expired: %d", rec.Code)
	}
}

func TestPickConnection(t *testing.T) {
	if got := pickConnection([]plexConnection{{URI: "https://r", Relay: true}}); got != "" {
		t.Errorf("relay chosen: %q", got)
	}
	got := pickConnection([]plexConnection{{Protocol: "https", URI: "https://remote"}, {Protocol: "http", URI: "http://lan", Local: true}})
	if got != "http://lan" {
		t.Errorf("got %q", got)
	}
}
