package plugins

// Epic 57 story 57.03, test case T10: plugin panels under the parental filter. A
// restricted child is shown a panel only on a subject it may see, and only of a
// plugin a rule allows for it (a panel is the plugin's own words and may name
// other titles). A hidden subject answers as a missing one.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

func TestPanelsUnderParentalFilter(t *testing.T) {
	f := newPanelFixture(t)
	tok := f.mint(t).Token
	f.put(t, tok, "loudness", `[{"label":"Integrated","value":"-14 LUFS"}]`)
	f.grant(t, []string{"artwork.write"}, []string{f.libA})
	tok = f.mint(t).Token
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	if code, c, _ := f.call(t, tok, "write/set-artwork", map[string]any{"subject": map[string]string{"kind": "video", "id": f.videoA}, "kind": "panel", "image": png}); code != 200 {
		t.Fatalf("panel image: %d %s", code, c)
	}
	f.exec(t, `UPDATE videos SET age_tier = 0 WHERE id = $1`, f.videoA)
	f.exec(t, `UPDATE videos SET age_tier = 16 WHERE id = $1`, f.videoB)

	const child = "00000000-0000-4000-8000-0000000000c1"
	limit := func(rules ...parental.Rule) *parental.Effective {
		return &parental.Effective{UserID: child, Restricted: true, MaxTier: parental.Tier9, Unrated: parental.UnratedHide, Rules: rules}
	}
	allowPlugin := parental.Rule{Effect: parental.EffectAllow, SubjectKind: parental.SubjectPlugin, SubjectID: testPlugin}
	get := func(e *parental.Effective, url string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		ctx := principal.WithPrincipal(req.Context(), &principal.Principal{UserID: child, Libraries: []string{f.libA}})
		if e != nil {
			ctx = parental.With(ctx, e)
		}
		w := httptest.NewRecorder()
		f.r.ServeHTTP(w, req.WithContext(ctx))
		return w.Code, w.Body.String()
	}
	list := "/api/panels?subject=video:" + f.videoA
	image := "/api/panels/image?plugin=" + testPlugin + "&subject=video:" + f.videoA

	// the same account with no limit sees the panel and its image
	if code, body := get(nil, list); code != 200 || !strings.Contains(body, "-14 LUFS") {
		t.Fatalf("with no limit: %d %s", code, body)
	}
	if code, _ := get(nil, image); code != 200 {
		t.Fatalf("image with no limit: %d", code)
	}

	// T10: an allowed video, a plugin not allowed for the child: no panel, no image
	if code, body := get(limit(), list); code != 200 || !strings.Contains(body, `"panels":[]`) || strings.Contains(body, "LUFS") {
		t.Errorf("the plugin is not allowed for the child: %d %s, want no panels", code, body)
	}
	if code, _ := get(limit(), image); code != 404 {
		t.Errorf("image of a plugin not allowed for the child: %d, want 404", code)
	}
	// an allow rule on another plugin, or an expired one, changes nothing
	other := parental.Rule{Effect: parental.EffectAllow, SubjectKind: parental.SubjectPlugin, SubjectID: "dev.example.other"}
	if _, body := get(limit(other), list); !strings.Contains(body, `"panels":[]`) {
		t.Errorf("an allow on another plugin shows this one's panel: %s", body)
	}
	// the plugin allowed: the panel and the image
	if code, body := get(limit(allowPlugin), list); code != 200 || !strings.Contains(body, "-14 LUFS") {
		t.Errorf("the plugin allowed for the child: %d %s", code, body)
	}
	if code, _ := get(limit(allowPlugin), image); code != 200 {
		t.Errorf("image of the allowed plugin: %d", code)
	}

	// a hidden subject is a missing one, whatever the plugin rule says
	for _, e := range []*parental.Effective{limit(), limit(allowPlugin)} {
		for _, path := range []string{"/api/panels?subject=video:", "/api/panels/image?plugin=" + testPlugin + "&subject=video:"} {
			hc, hb := get(e, path+f.videoB)
			mc, mb := get(e, path+"00000000-0000-4000-8000-00000000dead")
			if hc != 404 || hc != mc || hb != mb {
				t.Errorf("GET %s<hidden>: %d %s; a missing video: %d %s", path, hc, hb, mc, mb)
			}
		}
	}
	// a block on the video, on its library: hidden too
	for _, r := range []parental.Rule{
		{Effect: parental.EffectBlock, SubjectKind: parental.SubjectVideo, SubjectID: f.videoA},
		{Effect: parental.EffectBlock, SubjectKind: parental.SubjectLibrary, SubjectID: f.libA},
	} {
		if code, body := get(limit(allowPlugin, r), list); code != 404 || strings.Contains(body, "LUFS") {
			t.Errorf("with a block on the %s: %d %s", r.SubjectKind, code, body)
		}
	}
	if code, _ := get(limit(allowPlugin), "/api/panels?subject=library:"+f.libA); code != 200 {
		t.Errorf("the library's panels for the child: %d", code)
	}
	// a check that cannot be made refuses
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if subjectAllowed(parental.With(gone, limit()), f.db, "video", f.videoA) {
		t.Error("a check on a cancelled request allowed the subject")
	}
	// no one is asked to sign in, as before
	if code, _ := f.get(t, nil, list); code != 401 {
		t.Errorf("no one: %d", code)
	}
}
