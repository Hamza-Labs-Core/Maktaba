package videos

// Epic 45 Phase 2 on GET /api/videos: tag, speaker, favorite and letter
// filters, sort=random, and the caller's marks on each item.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"
)

func phase2Handler(t *testing.T) *Handler {
	t.Helper()
	db := extDB(t)
	for _, q := range []string{
		`CREATE TABLE video_tags (video_id TEXT, tag_id TEXT)`,
		`CREATE TABLE speakers (id TEXT, video_id TEXT, cluster_label TEXT, name TEXT)`,
		`INSERT INTO video_tags VALUES ('` + xvid(1) + `','00000000-0000-4000-8000-0000000000c1'), ('` + xvid(8) + `','00000000-0000-4000-8000-0000000000c1')`,
		`INSERT INTO speakers VALUES ('s1','` + xvid(2) + `','c0','Sheikh'), ('s2','` + xvid(9) + `','c0','Sheikh')`,
		`INSERT INTO user_favorites VALUES ('` + extUser + `','` + xvid(3) + `','x'), ('` + extUser + `','` + xvid(9) + `','x'), ('other','` + xvid(4) + `','x')`,
		// titles for the jump bar: Arabic, a digit, an empty one, lower and upper case
		`UPDATE videos SET title = 'باب' WHERE id = '` + xvid(5) + `'`,
		`UPDATE videos SET title = '2 lessons' WHERE id = '` + xvid(6) + `'`,
		`UPDATE videos SET title = NULL WHERE id = '` + xvid(7) + `'`,
		`UPDATE videos SET title = 'Bravo' WHERE id = '` + xvid(2) + `'`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return &Handler{DB: db}
}

func listBody(t *testing.T, h *Handler, ctx context.Context, p *principal.Principal, qs url.Values) (int, []Video, *string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/videos?"+qs.Encode(), nil)
	req = req.WithContext(principal.WithPrincipal(ctx, p))
	rr := httptest.NewRecorder()
	h.List(rr, req)
	var body struct {
		Items []Video `json:"items"`
		Next  *string `json:"next"`
	}
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
	return rr.Code, body.Items, body.Next
}

func vidIDs(vs []Video) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

func TestListPhase2Filters(t *testing.T) {
	h := phase2Handler(t)
	ctx := context.Background()
	q := func(kv ...string) url.Values {
		v := url.Values{"sort": {"title"}, "order": {"asc"}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	for _, c := range []struct {
		name string
		p    *principal.Principal
		qs   url.Values
		want []string
	}{
		{"tag, viewer of A", viewerA(), q("tag", "00000000-0000-4000-8000-0000000000c1"), []string{xvid(1)}},
		{"tag, admin", admin(), q("tag", "00000000-0000-4000-8000-0000000000c1"), []string{xvid(1), xvid(8)}},
		{"speaker, viewer of A", viewerA(), q("speaker", "Sheikh"), []string{xvid(2)}},
		{"favorite, viewer of A", viewerA(), q("favorite", "true"), []string{xvid(3)}},
		{"favorite, admin", admin(), q("favorite", "true"), []string{xvid(3), xvid(9)}},
		{"letter B, both cases", viewerA(), q("letter", "b"), []string{xvid(2)}},
		{"letter ب", viewerA(), q("letter", "ب"), []string{xvid(5)}},
		{"letter #: a digit and no title", viewerA(), q("letter", "#"), []string{xvid(7), xvid(6)}},
		{"letter # and a tag", viewerA(), q("letter", "#", "tag", "00000000-0000-4000-8000-0000000000c1"), []string{}},
	} {
		code, items, _ := listBody(t, h, ctx, c.p, c.qs)
		if code != http.StatusOK {
			t.Errorf("%s: status %d", c.name, code)
			continue
		}
		if got := vidIDs(items); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	for name, qs := range map[string]url.Values{
		"bad tag":              q("tag", "x"),
		"bad favorite":         q("favorite", "yes"),
		"two letters":          q("letter", "ab"),
		"random with a cursor": {"sort": {"random"}, "cursor": {"abc"}},
	} {
		if code, _, _ := listBody(t, h, ctx, viewerA(), qs); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
}

func TestListRandomAndMarks(t *testing.T) {
	h := phase2Handler(t)
	ctx := context.Background()
	code, items, next := listBody(t, h, ctx, viewerA(), url.Values{"sort": {"random"}, "limit": {"3"}})
	if code != http.StatusOK || len(items) != 3 || next != nil {
		t.Fatalf("random: %d %d items, next %v", code, len(items), next)
	}
	for _, v := range items {
		if v.LibraryID != extLibA {
			t.Errorf("random lists library B's %s", v.ID)
		}
	}
	code, items, _ = listBody(t, h, ctx, viewerA(), url.Values{"sort": {"title"}, "order": {"asc"}, "limit": {"50"}})
	if code != http.StatusOK {
		t.Fatal(code)
	}
	marks := map[string]Video{}
	for _, v := range items {
		marks[v.ID] = v
	}
	if ps := marks[xvid(1)].PlaybackState; ps == nil || !ps.Completed {
		t.Errorf("1 is watched: %+v", ps)
	}
	if ps := marks[xvid(2)].PlaybackState; ps == nil || ps.Completed || ps.PositionSec != 40 {
		t.Errorf("2 is under way: %+v", ps)
	}
	if marks[xvid(4)].PlaybackState != nil || marks[xvid(4)].Favorite != nil {
		t.Errorf("4: another user's marks shown: %+v", marks[xvid(4)])
	}
	if f := marks[xvid(3)].Favorite; f == nil || !*f {
		t.Errorf("3 is a favorite")
	}
}

func TestListFavoriteOnANode(t *testing.T) {
	h := phase2Handler(t)
	// served by a machine from its copy (placement any): no favorites table there
	if _, err := h.DB.Exec(`DROP TABLE user_favorites`); err != nil {
		t.Fatal(err)
	}
	ctx := placement.WithClass(context.Background(), placement.Any)
	if code, _, _ := listBody(t, h, ctx, viewerA(), url.Values{"favorite": {"true"}}); code != http.StatusBadRequest {
		t.Errorf("favorite on a node: %d, want 400", code)
	}
	code, items, _ := listBody(t, h, ctx, viewerA(), url.Values{"sort": {"title"}})
	if code != http.StatusOK || len(items) == 0 {
		t.Fatalf("plain list on a node: %d", code)
	}
	for _, v := range items {
		if v.Favorite != nil {
			t.Errorf("a favorite flag read on a node: %s", v.ID)
		}
	}
}
