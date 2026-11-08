package plugins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/relay"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
)

const plexSecret = "s3cr3t-PLEX-token-for-the-plugin-route"

// toyWithService is the toy first-party bundle whose manifest lists services.
func toyWithService(t *testing.T, services string) []byte {
	t.Helper()
	raw, err := os.ReadFile("firstparty/testdata/toy.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	p := doc["plugins"].([]any)[0].(map[string]any)
	m := p["manifest"].(string)
	p["manifest"] = strings.Replace(m, `settings  = ["groups"]`, `settings  = ["groups"]`+"\nservice   = "+services, 1)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Story 54.35: a first-party plugin's stage reaches Plex through the home's door. The token
// reaches Plex, never the plugin; a path off the allow-list is plugin_service_path; a
// service the manifest does not list is plugin_capability.
func TestAPluginReachesPlexThroughTheDoorWithoutTheToken(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	plex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" token="+r.Header.Get("X-Plex-Token"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"MediaContainer":{"Dvr":[{"key":"12","uri":"http://t/?X-Plex-Token=`+plexSecret+`"}],"note":"`+plexSecret+`"}}`)
	}))
	defer plex.Close()

	defer firstparty.UseBundle(toyWithService(t, `["plex"]`))()
	f := newFixture(t)
	f.s.Service = relay.New(relay.Config{
		// the relay for nodes switched off: the home's own plugin is served all the same
		Settings: func(context.Context) (relay.Settings, error) {
			st := relay.DefaultSettings()
			st.Enabled = false
			return st, nil
		},
		Upstream: func(context.Context, *relay.Extension) (relay.Upstream, error) {
			return relay.Upstream{Enabled: true, URL: plex.URL, Secret: plexSecret}, nil
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	f.exec(t, `INSERT INTO job_stages (stage, label, subject_kinds, pool, lane) VALUES ('fp_toy','Toy','{library}','cpu','seasons')`)
	var job int64
	if err := f.db.QueryRow(`INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, library_id)
		VALUES ('fp_toy','running','library',$1,$1) RETURNING id`, f.libA).Scan(&job); err != nil {
		t.Fatal(err)
	}
	m, err := f.s.Tokens.Mint(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	call := func(service, method, target string) (int, string, []byte) {
		return f.call(t, m.Token, "trusted/service", map[string]any{"service": service, "method": method,
			"target": target, "accept": "application/json", "body": ""})
	}

	code, c, body := call("plex", "GET", "/livetv/dvrs")
	if code != 200 {
		t.Fatalf("an allowed call: %d %s %s", code, c, body)
	}
	var ans serviceAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatal(err)
	}
	got, _ := base64.StdEncoding.DecodeString(ans.Body)
	if ans.Status != 200 || !strings.Contains(string(got), `"key":"12"`) {
		t.Fatalf("answer %+v %s", ans, got)
	}
	if strings.Contains(string(body), plexSecret) || strings.Contains(string(got), plexSecret) ||
		strings.Contains(strings.ToLower(string(got)), "x-plex-token") {
		t.Fatalf("the plugin saw the token: %s", got)
	}
	mu.Lock()
	if len(seen) != 1 || seen[0] != "GET /livetv/dvrs token="+plexSecret {
		t.Fatalf("plex saw %v", seen)
	}
	mu.Unlock()

	if code, c, _ := call("plex", "GET", "/livetv/sessions"); code != 403 || c != CodeServicePath {
		t.Fatalf("off the allow-list: %d %s", code, c)
	}
	if code, c, _ := call("plex", "GET", "/livetv/dvrs?X-Plex-Token=mine"); code != 403 || c != CodeServicePath {
		t.Fatalf("a token in the query: %d %s", code, c)
	}
	if code, c, _ := call("cloudflare_stream", "GET", "/accounts"); code != 403 || c != CodeCapability {
		t.Fatalf("a service the manifest does not list: %d %s", code, c)
	}

	// a joined machine's plugin API has no door: its stages ask their worker
	f.s.Service = nil
	if code, c, _ := call("plex", "GET", "/livetv/dvrs"); code != 503 || c != relay.CodeOff {
		t.Fatalf("no door: %d %s", code, c)
	}
}
