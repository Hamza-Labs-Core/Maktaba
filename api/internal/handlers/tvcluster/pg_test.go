package tvcluster

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The same read and write against real Postgres (slot 0121's types: jsonb, bool,
// timestamptz). Runs only with MAKTABA_TEST_PG_URL.
func TestClusterPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ('6f1c9a2e-0000-4000-8000-000000000001', 101, 'One', 'one', 'shuffle')`)
	exec(`SELECT tv_host_beat('h', '{"host":"mac","role":"home","capacity":16,"encoders":["videotoolbox"],"load":{"protocol":1,"viewers":{"6f1c9a2e-0000-4000-8000-000000000001":{"hls":1,"tuners":0}}},"can_push":{"rtmps":true}}'::jsonb)`)
	exec(`SELECT tv_host_beat('n', '{"host":"pc","role":"node","capacity":3,"load":{"protocol":1}}'::jsonb)`)
	exec(`INSERT INTO channel_placements (channel_id, host_id, generation, state, reason)
		VALUES ('6f1c9a2e-0000-4000-8000-000000000001', 'h', nextval('tv_placement_generation_seq'), 'live', 'first_request')`)
	h := New(db)
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, admin(httptest.NewRequest("GET", "/api/tv/cluster", nil)))
	if rec.Code != 200 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	var resp Response
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Hosts) != 2 || len(resp.OnAir) != 1 || resp.OnAir[0].Viewers.HLS != 1 || resp.OnAir[0].HostName != "mac" {
		t.Fatalf("resp %+v", resp)
	}
	for _, x := range resp.Hosts {
		switch x.HostID {
		case "h":
			if x.State != "online" || !x.Enabled || x.Used != 1 || !x.CanPush["rtmps"] {
				t.Errorf("home %+v", x)
			}
		case "n":
			if x.State != "disabled" || x.Enabled {
				t.Errorf("a new node is disabled: %+v", x)
			}
		}
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, admin(httptest.NewRequest("PUT", "/api/tv/cluster/hosts/n",
		jsonBody(`{"enabled":true,"capacity_override":2,"stream_base_url":"http://10.0.0.2:8081"}`))))
	if rec.Code != 200 {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	// the node's next beat keeps the admin's address
	exec(`SELECT tv_host_beat('n', '{"host":"pc","role":"node","capacity":3,"stream_base_url":"http://169.254.1.1:8081"}'::jsonb)`)
	var base, src string
	if err := db.QueryRow(`SELECT stream_base_url, stream_base_url_source FROM streaming_hosts WHERE host_id = 'n'`).Scan(&base, &src); err != nil ||
		base != "http://10.0.0.2:8081" || src != "admin" {
		t.Errorf("admin address replaced by the beat: %q %q %v", base, src, err)
	}
}
