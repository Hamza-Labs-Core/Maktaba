package system

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Story 59.12 (T07's data): which release runs on the Docker image, and why.
func TestContainerImage(t *testing.T) {
	env := func(image, refused string) func(string) string {
		return func(k string) string {
			switch k {
			case EnvImageVersion:
				return image
			case EnvImageRefused:
				return refused
			}
			return ""
		}
	}
	for _, c := range []struct {
		name, runtime, image, refused string
		want                          *ContainerImage
	}{
		{"same", "v1.5.0", "1.5.0", "0", &ContainerImage{Runtime: "1.5.0", Image: "1.5.0", Running: "image", Reason: ImageSame}},
		{"an older image", "1.6.0", "v1.5.0", "0", &ContainerImage{Runtime: "1.6.0", Image: "1.5.0", Running: "runtime", Reason: ImageOlder}},
		{"a refused image", "1.5.0", "1.6.0", "1", &ContainerImage{Runtime: "1.5.0", Image: "1.6.0", Running: "runtime", Reason: ImageRefused}},
		{"a newer image not yet on the volume", "1.5.0", "1.6.0", "0", &ContainerImage{Runtime: "1.5.0", Image: "1.6.0", Running: "runtime", Reason: ImagePending}},
		{"a describe build ahead of its tag", "0.3.123-4-gabc1234", "0.3.123", "0", &ContainerImage{Runtime: "0.3.123-4-gabc1234", Image: "0.3.123", Running: "runtime", Reason: ImageOlder}},
		{"not the image", "1.5.0", "", "", nil},
	} {
		got := containerImage(c.runtime, env(c.image, c.refused))
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

// The container block goes to an admin only: GET /api/system/updates stays open to every
// signed-in user, without it (the role test of story 59.16's checklist).
func TestStatusHandlerContainerBlockIsAdminsOnly(t *testing.T) {
	t.Setenv(EnvImageVersion, "1.4.0")
	t.Setenv(EnvImageRefused, "0")
	su := NewSelfUpdater(NewUpdater(UpdaterConfig{CurrentVersion: "v1.5.0", Disabled: true}))
	base := fixedStatus(`{"available":false,"current_version":"v1.5.0","channel":"stable"}`)
	get := func(p *principal.Principal) map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/system/updates", nil)
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		su.StatusHandler(base).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	doc := get(&principal.Principal{UserID: "a", IsAdmin: true})
	c, _ := doc["container"].(map[string]any)
	if c == nil || c["runtime"] != "1.5.0" || c["image"] != "1.4.0" || c["running"] != "runtime" || c["reason"] != ImageOlder {
		t.Fatalf("admin: %v", doc)
	}
	for name, p := range map[string]*principal.Principal{"a user": {UserID: "u"}, "no principal": nil} {
		if doc := get(p); doc["container"] != nil {
			t.Errorf("%s sees the container block: %v", name, doc["container"])
		}
	}
}
