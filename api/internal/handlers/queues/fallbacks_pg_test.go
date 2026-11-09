package queues

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Story 54.43 T04 and T05: the counted in-process fallbacks, read by an admin (every row,
// the machine's name, the total; ?stage= narrows), and refused to no one (401), a viewer
// and a cloud-linked play sign-in (403).
func TestFallbacksReadAndRoles(t *testing.T) {
	r, db := extRouter(t)
	if _, err := db.Exec(`INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey)
		VALUES ('00000000-0000-0000-0000-0000000000d1', 'Den PC', 'approved', 'linux', 'amd64', 'k')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO stage_fallbacks (machine, stage, reason, count, first_at, last_at) VALUES
		('home', 'probe', 'runner_not_serving_here', 3, now() - interval '2 hours', now() - interval '1 hour'),
		('00000000-0000-0000-0000-0000000000d1', 'thumb_hash', 'fingerprint_inproc', 2, now() - interval '1 hour', now()),
		('home', 'plex_index', 'role_unusable', 9, now(), now())`); err != nil {
		t.Fatal(err)
	}

	play := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000b1", IsAdmin: true, AccessAllLibraries: true}
	play.ApplyCloudScope(principal.CloudScopePlay, nil)
	viewer := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1"}
	for _, c := range []struct {
		who  string
		p    *principal.Principal
		want int
	}{
		{"no one", nil, http.StatusUnauthorized},
		{"viewer", viewer, http.StatusForbidden},
		{"cloud play", play, http.StatusForbidden},
	} {
		rec := extCall(t, r, c.p, "GET", "/api/queues/fallbacks", nil)
		if rec.Code != c.want {
			t.Errorf("as %s: %d, want %d", c.who, rec.Code, c.want)
		}
		if json.Valid(rec.Body.Bytes()) && rec.Code != http.StatusOK && containsRows(rec.Body.Bytes()) {
			t.Errorf("as %s: rows leaked: %s", c.who, rec.Body.String())
		}
	}

	rec := extCall(t, r, extAdmin(), "GET", "/api/queues/fallbacks", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin: %d %s", rec.Code, rec.Body.String())
	}
	var got FallbacksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// the retired plex_index is left out; newest first
	if len(got.Fallbacks) != 2 || got.Total != 5 {
		t.Fatalf("answered %+v", got)
	}
	node, home := got.Fallbacks[0], got.Fallbacks[1]
	if node.Stage != "thumb_hash" || node.Reason != "fingerprint_inproc" || node.Count != 2 ||
		node.MachineName == nil || *node.MachineName != "Den PC" {
		t.Fatalf("node row %+v", node)
	}
	if home.Machine != "home" || home.MachineName != nil || home.Stage != "probe" || home.Count != 3 ||
		!home.FirstAt.Before(home.LastAt) {
		t.Fatalf("home row %+v", home)
	}

	rec = extCall(t, r, extAdmin(), "GET", "/api/queues/fallbacks?stage=probe", nil)
	got = FallbacksResponse{}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || len(got.Fallbacks) != 1 || got.Fallbacks[0].Stage != "probe" || got.Total != 3 {
		t.Fatalf("narrowed: %d %s", rec.Code, rec.Body.String())
	}
}

func containsRows(body []byte) bool {
	var v map[string]any
	if json.Unmarshal(body, &v) != nil {
		return false
	}
	_, ok := v["fallbacks"]
	return ok
}
