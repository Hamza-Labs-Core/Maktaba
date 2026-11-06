package review

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const (
	libA   = "11111111-1111-4111-8111-111111111111"
	someID = "22222222-2222-4222-8222-222222222222"
)

func path(p string) string {
	return strings.NewReplacer("{id}", someID).Replace(p)
}

// Every word review route, as nobody (401), as a viewer, as a user with write or the
// library admin role on the library, and as a cloud-linked sign-in with manage granted
// (403 each): only a home admin reviews (README §8, decision 1). The checks come before
// any database read, so none is needed here; the home admin's 200s are in the
// Postgres tests.
func TestReviewRoles(t *testing.T) {
	h := &Handler{}
	r := chi.NewRouter()
	h.Mount(r)
	callers := []struct {
		name string
		p    *principal.Principal
		want int
	}{
		{"nobody", nil, http.StatusUnauthorized},
		{"viewer", &principal.Principal{UserID: "v", Libraries: []string{libA}}, http.StatusForbidden},
		{"write on the library", &principal.Principal{UserID: "w", Libraries: []string{libA}}, http.StatusForbidden},
		{"library admin role", &principal.Principal{UserID: "la", Libraries: []string{libA}, AccessAllLibraries: true}, http.StatusForbidden},
		{"cloud play-scoped, manage granted", &principal.Principal{UserID: "c", AccessAllLibraries: true,
			CloudScope: principal.CloudScopeManage}, http.StatusForbidden},
	}
	if len(Routes) < 19 {
		t.Fatalf("%d routes", len(Routes))
	}
	for _, rt := range Routes {
		for _, c := range callers {
			req := httptest.NewRequest(rt.Method, path(rt.Path), strings.NewReader(`{}`))
			if c.p != nil {
				req = req.WithContext(principal.WithPrincipal(req.Context(), c.p))
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("%s %s as %s: %d, want %d", rt.Method, rt.Path, c.name, rec.Code, c.want)
			}
		}
	}
}

// canReview never reads library_acl: a library role can never grant review.
func TestCanReviewNeverReadsTheLibraryACL(t *testing.T) {
	src, err := os.ReadFile("access.go")
	if err != nil {
		t.Fatal(err)
	}
	var code strings.Builder
	for _, line := range strings.Split(string(src), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		code.WriteString(line + "\n")
	}
	if strings.Contains(code.String(), "library_acl") || strings.Contains(code.String(), "Libraries") {
		t.Fatal("access.go reads the library ACL")
	}
	if CanReview(nil) || CanReview(&principal.Principal{AccessAllLibraries: true}) || !CanReview(&principal.Principal{IsAdmin: true}) {
		t.Fatal("CanReview is not 'a home admin'")
	}
}

// Every mounted route is in Routes (the role test above loops over it).
func TestRoutesListsEveryMount(t *testing.T) {
	src, err := os.ReadFile("review.go")
	if err != nil {
		t.Fatal(err)
	}
	mounts := regexp.MustCompile(`r\.(Get|Post|Put|Delete)\("([^"]+)"`).FindAllStringSubmatch(string(src), -1)
	listed := map[string]bool{}
	for _, rt := range Routes {
		listed[rt.Method+" "+rt.Path] = true
	}
	for _, m := range mounts {
		if !listed[strings.ToUpper(m[1])+" "+m[2]] {
			t.Errorf("%s %s is not in Routes", m[1], m[2])
		}
	}
	if len(mounts) != len(Routes) {
		t.Errorf("%d mounts, %d routes", len(mounts), len(Routes))
	}
}

// The reason, status and source codes are the pipeline's and the web's (one list each).
func TestCodesMatchThePipelineAndTheWeb(t *testing.T) {
	py, err := os.ReadFile("../../../../pipeline/src/maktaba_pipeline/word_review/settings.py")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := os.ReadFile("../../../../web/src/lib/reviewApi.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		codes []string
	}{{"REASONS", Reasons}, {"STATUSES", Statuses}, {"SOURCES", Sources}} {
		for _, src := range []struct {
			file string
			re   *regexp.Regexp
		}{
			{"settings.py", regexp.MustCompile(`(?s)` + c.name + `: tuple\[str, \.\.\.\] = \((.*?)\)`)},
			{"reviewApi.ts", regexp.MustCompile(`(?s)export const ` + c.name + ` = \[(.*?)\]`)},
		} {
			body := string(py)
			if src.file == "reviewApi.ts" {
				body = string(ts)
			}
			m := src.re.FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("%s: no %s", src.file, c.name)
			}
			got := regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(m[1], -1)
			var names []string
			for _, g := range got {
				names = append(names, g[1])
			}
			if strings.Join(names, ",") != strings.Join(c.codes, ",") {
				t.Errorf("%s %s = %v, Go has %v", src.file, c.name, names, c.codes)
			}
		}
	}
}
