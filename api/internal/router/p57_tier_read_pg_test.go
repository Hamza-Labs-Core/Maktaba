package router_test

// Epic 57: the age tier an admin set comes back in the GET of the thing it was set
// on, for an admin alone. Each route is tried as no one, a viewer, a restricted
// child, that child's guardian, another guardian and an admin: only the admin's
// answer has the field (a tier, or null when none is set); for everyone else it is
// absent, whatever else their answer is.

import (
	"encoding/json"
	"net/http"
	"testing"
)

const trSource = "00000000-0000-4000-8000-000000574501"

// tierIn finds the object the route answers about and reports its field: present,
// and its JSON value. list names the array the object sits in ("" for none).
func tierIn(t *testing.T, body, list, id, field string) (bool, any) {
	t.Helper()
	var top map[string]any
	if err := json.Unmarshal([]byte(body), &top); err != nil {
		t.Fatalf("not JSON: %v: %s", err, body)
	}
	obj := top
	if list != "" {
		obj = nil
		items, _ := top[list].([]any)
		for _, it := range items {
			if m, ok := it.(map[string]any); ok && m["id"] == id {
				obj = m
			}
		}
		if obj == nil {
			return false, nil
		}
	}
	v, ok := obj[field]
	return ok, v
}

func TestP57TierReadBack(t *testing.T) {
	e := setupFilter(t)
	e.exec(`INSERT INTO youtube_sources (id, library_id, kind, url, external_id) VALUES ($1, $2, 'channel', 'https://example.test/tr', 'UCtr')`, trSource, pfLibA)

	routes := []struct {
		name, path, list, id, field string
		// set stores a tier the way the admin's PUT does; clear removes it
		set, clear string
		// adminOnly: the route itself is the admin's (anyone else gets 403)
		adminOnly bool
	}{
		{"video", "/api/videos/" + pfKid, "", pfKid, "age_tier_admin",
			`UPDATE videos SET age_tier_admin = 9 WHERE id = '` + pfKid + `'`,
			`UPDATE videos SET age_tier_admin = NULL WHERE id = '` + pfKid + `'`, false},
		{"series", "/api/series/" + pfSeries, "", pfSeries, "age_tier",
			`UPDATE series SET age_tier = 9 WHERE id = '` + pfSeries + `'`,
			`UPDATE series SET age_tier = NULL WHERE id = '` + pfSeries + `'`, false},
		{"collection", "/api/collections/" + pfCol, "", pfCol, "age_tier",
			`UPDATE collections SET age_tier = 9 WHERE id = '` + pfCol + `'`,
			`UPDATE collections SET age_tier = NULL WHERE id = '` + pfCol + `'`, false},
		{"library", "/api/libraries/" + pfLibA, "", pfLibA, "age_tier",
			`UPDATE libraries SET age_tier = 9 WHERE id = '` + pfLibA + `'`,
			`UPDATE libraries SET age_tier = NULL WHERE id = '` + pfLibA + `'`, false},
		{"library (manage)", "/api/libraries/manage", "items", pfLibA, "age_tier",
			`UPDATE libraries SET age_tier = 9 WHERE id = '` + pfLibA + `'`,
			`UPDATE libraries SET age_tier = NULL WHERE id = '` + pfLibA + `'`, true},
		{"youtube source", "/api/libraries/" + pfLibA + "/youtube/sources", "items", trSource, "age_tier",
			`UPDATE youtube_sources SET age_tier = 9 WHERE id = '` + trSource + `'`,
			`UPDATE youtube_sources SET age_tier = NULL WHERE id = '` + trSource + `'`, false},
	}
	others := []string{"viewer", "child", "guardian", "other-guardian"}

	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			e.exec(rt.set)
			t.Cleanup(func() { e.exec(rt.clear) })

			// no one: refused before anything is read
			if code, body := e.get("", rt.path); code == http.StatusOK {
				t.Fatalf("no one got 200: %s", body)
			}

			// an admin: the tier that was set
			code, body := e.get("admin", rt.path)
			if code != http.StatusOK {
				t.Fatalf("admin: %d %s", code, body)
			}
			has, v := tierIn(t, body, rt.list, rt.id, rt.field)
			if !has || v != float64(9) {
				t.Fatalf("admin: %s = %v (present %v), want 9: %s", rt.field, v, has, body)
			}

			// everyone else: no field, whether or not the route answers them
			for _, who := range others {
				code, body := e.get(who, rt.path)
				if rt.adminOnly {
					if code != http.StatusForbidden {
						t.Errorf("%s: %d, want 403", who, code)
					}
					continue
				}
				if code != http.StatusOK {
					// refused or hidden: nothing of the tier can be in a problem body
					var p map[string]any
					_ = json.Unmarshal([]byte(body), &p)
					if _, leaked := p[rt.field]; leaked {
						t.Errorf("%s: a %d answer carries %s: %s", who, code, rt.field, body)
					}
					continue
				}
				if has, v := tierIn(t, body, rt.list, rt.id, rt.field); has {
					t.Errorf("%s: %s = %v, want the field absent: %s", who, rt.field, v, body)
				}
			}
		})
	}

	// nothing set: an admin reads null, not an absent field (the control shows
	// "no rating", not "choose")
	for _, rt := range routes {
		code, body := e.get("admin", rt.path)
		if code != http.StatusOK {
			t.Fatalf("%s: admin %d", rt.name, code)
		}
		if has, v := tierIn(t, body, rt.list, rt.id, rt.field); !has || v != nil {
			t.Errorf("%s with no tier: %s = %v (present %v), want null", rt.name, rt.field, v, has)
		}
	}

	// the viewer and the child do read the things themselves: the test above is not
	// passing only because they were refused
	for _, who := range []string{"viewer", "child"} {
		for _, path := range []string{"/api/videos/" + pfKid, "/api/series/" + pfSeries, "/api/collections/" + pfCol, "/api/libraries/" + pfLibA} {
			if code, body := e.get(who, path); code != http.StatusOK {
				t.Errorf("%s GET %s: %d %s", who, path, code, body)
			}
		}
	}
}

// The same for a TV channel, on the live TV routes: one channel and the list.
func TestP57TierReadBackChannel(t *testing.T) {
	e := setupLive(t)
	for _, rt := range []struct{ name, path, list string }{
		{"one", "/api/channels/" + e.ch9, ""},
		{"list", "/api/channels", "items"},
	} {
		t.Run(rt.name, func(t *testing.T) {
			if code, body, _ := e.do("", http.MethodGet, rt.path, ""); code == http.StatusOK {
				t.Fatalf("no one got 200: %s", body)
			}
			code, body, _ := e.do("admin", http.MethodGet, rt.path, "")
			if code != http.StatusOK {
				t.Fatalf("admin: %d %s", code, body)
			}
			if has, v := tierIn(t, body, rt.list, e.ch9, "age_tier"); !has || v != float64(9) {
				t.Fatalf("admin: age_tier = %v (present %v), want 9: %s", v, has, body)
			}
			// a channel with no tier: null for the admin, who alone sees it at all
			if has, v := tierIn(t, e.must(t, "admin", "/api/channels"), "items", e.chNone, "age_tier"); !has || v != nil {
				t.Errorf("admin, unrated channel: age_tier = %v (present %v), want null", v, has)
			}
			// a viewer, the child (the channel is within its limit), its guardian and
			// another guardian all read the channel, without the field
			for _, who := range []string{"viewer", "child", "guardian", "other"} {
				code, body, _ := e.do(who, http.MethodGet, rt.path, "")
				if code != http.StatusOK {
					t.Errorf("%s: %d %s", who, code, body)
					continue
				}
				if rt.list != "" {
					var top struct {
						Items []map[string]any `json:"items"`
					}
					_ = json.Unmarshal([]byte(body), &top)
					if len(top.Items) == 0 {
						t.Errorf("%s: an empty list proves nothing", who)
					}
					for _, it := range top.Items {
						if _, leaked := it["age_tier"]; leaked {
							t.Errorf("%s: a listed channel carries age_tier: %v", who, it)
						}
					}
					continue
				}
				if has, v := tierIn(t, body, "", e.ch9, "age_tier"); has {
					t.Errorf("%s: age_tier = %v, want the field absent", who, v)
				}
			}
		})
	}
}

func (e *liveEnv) must(t *testing.T, who, path string) string {
	t.Helper()
	code, body, _ := e.do(who, http.MethodGet, path, "")
	if code != http.StatusOK {
		t.Fatalf("%s GET %s: %d %s", who, path, code, body)
	}
	return body
}
