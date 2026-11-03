package components

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
)

var bundleRoutes = []struct{ method, path, body string }{
	{"GET", "/api/components/bundles", ""},
	{"POST", "/api/components/bundle", `{"bundle":"core","dry_run":true}`},
	{"POST", "/api/components/bundle", `{"bundle":"core"}`},
}

// T05: no one 401, a viewer and a play sign-in 403 (nothing changes), an admin 200.
func TestBundleRoutesAdminOnly(t *testing.T) {
	f := newFixture(t)
	rev := f.revision(t)
	for _, c := range bundleRoutes {
		if rec := f.do(c.method, c.path, c.body, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s as no one: %d", c.method, c.path, rec.Code)
		}
		for name, p := range map[string]*principal.Principal{"viewer": viewer(), "play": playAdmin(), "token": token(), "machine": machine()} {
			if rec := f.do(c.method, c.path, c.body, p); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d", c.method, c.path, name, rec.Code)
			}
		}
	}
	if f.revision(t) != rev || f.audits(t) != 0 {
		t.Fatalf("refused calls changed rows: revision %d -> %d, %d audit rows", rev, f.revision(t), f.audits(t))
	}
	for _, c := range bundleRoutes {
		if rec := f.do(c.method, c.path, c.body, admin()); rec.Code != http.StatusOK {
			t.Errorf("%s %s as admin: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

// T04 and the AC: a Core home asks for Full with dry_run: every component to enable and
// every asset to download with its bytes, and nothing changes; without dry_run all of it
// changes in one batch. Back to Core: turned off, removed only with remove_unused.
func TestBundleDiffAndApply(t *testing.T) {
	f := newFixture(t)
	var core svc.BundleDiff
	decode(t, f.do("POST", "/api/components/bundle", `{"bundle":"core"}`, admin()), http.StatusOK, &core)
	if len(core.Remove) != 0 || !slices.Contains(core.Disable, "transcription") || !slices.Contains(core.Disable, "runtime.ml") {
		t.Fatalf("full -> core: %+v", core)
	}
	if f.want(t, "transcription").Want != "installed" || f.want(t, "runtime.ml").Want != "installed" {
		t.Fatal("core was not applied")
	}
	// the core home's assets are gone (as a Core setup leaves them)
	var gone svc.BundleDiff
	decode(t, f.do("POST", "/api/components/bundle", `{"bundle":"core","remove_unused":true}`, admin()), http.StatusOK, &gone)
	if !slices.Contains(gone.Remove, "runtime.ml") || !slices.Contains(gone.Remove, "model.whisper-turbo") || len(gone.Enable) != 0 {
		t.Fatalf("remove_unused: %+v", gone)
	}
	if f.want(t, "runtime.ml").Want != "absent" {
		t.Fatal("remove_unused left runtime.ml")
	}

	var list struct {
		BasedOn string `json:"based_on"`
		Bundles []struct {
			ID            string `json:"id"`
			DownloadBytes int64  `json:"download_bytes"`
		} `json:"bundles"`
	}
	decode(t, f.do("GET", "/api/components/bundles", "", admin()), http.StatusOK, &list)
	if list.BasedOn != "core" || len(list.Bundles) != 3 {
		t.Fatalf("bundles %+v", list)
	}

	rev, audits := f.revision(t), f.audits(t)
	var dry svc.BundleDiff
	decode(t, f.do("POST", "/api/components/bundle", `{"bundle":"full","dry_run":true}`, admin()), http.StatusOK, &dry)
	if dry.BasedOn != "core" || dry.To != "full" || !dry.DryRun {
		t.Fatalf("dry run %+v", dry)
	}
	for _, id := range []string{"transcription", "search.semantic", "llm", "livetv"} {
		if !slices.Contains(dry.Enable, id) {
			t.Errorf("enable lacks %s: %v", id, dry.Enable)
		}
	}
	bytes := map[string]int64{}
	for _, a := range dry.Install {
		bytes[a.ID] = a.Bytes
	}
	if bytes["runtime.ml"] <= 0 || bytes["model.whisper-turbo"] < 1_500_000_000 || dry.DownloadBytes < bytes["model.whisper-turbo"] {
		t.Fatalf("install %+v, download %d", dry.Install, dry.DownloadBytes)
	}
	if f.revision(t) != rev || f.audits(t) != audits {
		t.Fatal("dry_run changed rows")
	}

	var applied svc.BundleDiff
	decode(t, f.do("POST", "/api/components/bundle", `{"bundle":"full"}`, admin()), http.StatusOK, &applied)
	if applied.BatchID == "" || len(applied.Changed) == 0 {
		t.Fatalf("applied %+v", applied)
	}
	if f.want(t, "transcription").Want != "enabled" || f.want(t, "runtime.ml").Want != "enabled" {
		t.Fatal("full was not applied")
	}
	var batches int
	if err := f.db.QueryRow(`SELECT COUNT(DISTINCT json_extract(payload, '$.batch_id')) FROM audit_log
		WHERE category = 'components' AND action = 'component.bundle_apply' AND id > (SELECT MAX(id) - ? FROM audit_log)`,
		len(applied.Changed)).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if batches != 1 {
		t.Fatalf("the full apply's rows are in %d batches", batches)
	}
}

func TestBundleRefusesAnUnknownOne(t *testing.T) {
	f := newFixture(t)
	code(t, f.do("POST", "/api/components/bundle", `{"bundle":"huge","dry_run":true}`, admin()), http.StatusBadRequest, "bad_bundle")
	code(t, f.do("POST", "/api/components/bundle", `{"bundle":"core","generation":999}`, admin()), http.StatusConflict, "stale_generation")
}
