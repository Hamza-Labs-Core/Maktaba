package httperror

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is the checkout's root (this package is api/internal/httperror).
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// T05: the embedded copy is the shared registry, byte for byte (`go generate` refreshes it).
func TestEmbeddedRegistryIsCurrent(t *testing.T) {
	shared, err := os.ReadFile(filepath.Join(repoRoot(t), "shared", "api", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shared, registryJSON) {
		t.Fatal("api/internal/httperror/registry_gen.json differs from shared/api/problems.json: run `go generate ./internal/httperror` in api/")
	}
}

// shippedUnderscoreSlugs are the four slugs that shipped with underscores; clients match
// them (PlayQueue.kt reads empty_source), so they keep their spelling.
var shippedUnderscoreSlugs = map[string]bool{
	"smart_playlist": true, "automatic_collection": true, "stale_queue": true, "empty_source": true,
}

var (
	slugRE     = regexp.MustCompile(`^[a-z0-9_-]+$`)
	codeRE     = regexp.MustCompile(`^[a-z0-9_.-]+$`)
	audiences  = map[string]bool{"anyone": true, "person": true, "admin": true, "app": true, "node": true}
	helpRE     = regexp.MustCompile(`^[a-z0-9-]+#[a-z0-9-]+$`)
	anchorFmt  = "{#%s}"
	latinWords = regexp.MustCompile(`[A-Za-z]{2,}`)
	codeSpan   = regexp.MustCompile("`[^`]*`")
)

// lintEntries returns every rule an entry list breaks: the slug's spelling (a new slug
// with an underscore is refused, T13), the fields, both languages of every text, and one
// entry per slug and per code.
func lintEntries(es []Entry) []string {
	var bad []string
	slugs := map[string]bool{}
	codes := map[string]string{}
	for _, e := range es {
		at := e.Slug
		if !slugRE.MatchString(e.Slug) {
			bad = append(bad, fmt.Sprintf("%s: slug is not [a-z0-9_-]+", at))
		}
		if strings.Contains(e.Slug, "_") && !shippedUnderscoreSlugs[e.Slug] {
			bad = append(bad, fmt.Sprintf("%s: a new slug uses hyphens, not underscores", at))
		}
		if slugs[e.Slug] {
			bad = append(bad, fmt.Sprintf("%s: slug listed twice", at))
		}
		slugs[e.Slug] = true
		if e.Status < 400 || e.Status > 599 {
			bad = append(bad, fmt.Sprintf("%s: status %d is not an error status", at, e.Status))
		}
		if !audiences[e.Audience] {
			bad = append(bad, fmt.Sprintf("%s: audience %q is not one of anyone, person, admin, app, node", at, e.Audience))
		}
		if !slugRE.MatchString(e.Area) {
			bad = append(bad, fmt.Sprintf("%s: area %q", at, e.Area))
		}
		if e.Codes == nil {
			bad = append(bad, fmt.Sprintf("%s: codes is missing (use [] for none)", at))
		}
		for _, c := range e.Codes {
			if !codeRE.MatchString(c) {
				bad = append(bad, fmt.Sprintf("%s: code %q", at, c))
			}
			if other, ok := codes[c]; ok {
				bad = append(bad, fmt.Sprintf("%s: code %q is already on %s (a code belongs to one entry)", at, c, other))
			}
			codes[c] = e.Slug
		}
		for _, f := range []struct {
			name string
			t    Text
		}{{"title", e.Title}, {"means", e.Means}, {"do", e.Do}} {
			if strings.TrimSpace(f.t.En) == "" {
				bad = append(bad, fmt.Sprintf("%s: %s.en is empty", at, f.name))
			}
			if strings.TrimSpace(f.t.Ar) == "" {
				bad = append(bad, fmt.Sprintf("%s: %s.ar is empty", at, f.name))
			}
		}
		if e.Help != nil && !helpRE.MatchString(*e.Help) {
			bad = append(bad, fmt.Sprintf("%s: help %q is not topic#anchor", at, *e.Help))
		}
	}
	return bad
}

func TestRegistryEntriesAreWhole(t *testing.T) {
	if len(Entries()) == 0 {
		t.Fatal("the registry is empty")
	}
	for _, b := range lintEntries(Entries()) {
		t.Error(b)
	}
}

// T13: a new slug with an underscore is refused; the four shipped ones pass.
func TestLintRefusesNewUnderscoreSlugs(t *testing.T) {
	ok := Entries()[0]
	mk := func(slug string) Entry { e := ok; e.Slug = slug; e.Codes = []string{}; return e }
	if bad := lintEntries([]Entry{mk("smart_playlist"), mk("automatic_collection"), mk("stale_queue"), mk("empty_source")}); len(bad) != 0 {
		t.Fatalf("the shipped underscore slugs are refused: %v", bad)
	}
	bad := lintEntries([]Entry{mk("new_thing")})
	if len(bad) != 1 || !strings.Contains(bad[0], "new_thing: a new slug uses hyphens") {
		t.Fatalf("a new underscore slug: %v", bad)
	}
}

// The Arabic of every text is Arabic: its Latin words (product names) stay under a fifth of
// its words, the bar maktaba.dev and maktaba.tv's pages use. What a person types (a file name,
// a setting, a request), in backquotes, counts as code and not as words.
func TestRegistryArabicIsArabic(t *testing.T) {
	for _, e := range Entries() {
		for name, s := range map[string]string{"title": e.Title.Ar, "means": e.Means.Ar, "do": e.Do.Ar} {
			s = codeSpan.ReplaceAllString(s, "")
			words := len(strings.Fields(s))
			latin := len(latinWords.FindAllString(s, -1))
			if words > 0 && latin*5 > words {
				t.Errorf("%s: %s.ar is %d/%d Latin words", e.Slug, name, latin, words)
			}
		}
	}
}

// Every help link names an Epic 41 topic that has the anchor.
func TestRegistryHelpLinksResolve(t *testing.T) {
	for _, e := range Entries() {
		if e.Help == nil {
			continue
		}
		topic, anchor, _ := strings.Cut(*e.Help, "#")
		for _, lang := range []string{"en", "ar"} {
			b, err := os.ReadFile(filepath.Join(repoRoot(t), "web", "src", "help", "content", lang, topic+".md"))
			if err != nil {
				t.Errorf("%s: help topic %s (%s): %v", e.Slug, topic, lang, err)
				continue
			}
			if !strings.Contains(string(b), fmt.Sprintf(anchorFmt, anchor)) {
				t.Errorf("%s: help topic %s (%s) has no {#%s}", e.Slug, topic, lang, anchor)
			}
		}
	}
}

func TestTypeAndTypeForCode(t *testing.T) {
	if got := Type("seat-limit"); got != "https://maktaba.tv/problems/seat-limit" {
		t.Fatalf("Type: %q", got)
	}
	for _, e := range Entries() {
		for _, c := range e.Codes {
			if got := TypeForCode(c); got != Type(e.Slug) {
				t.Errorf("TypeForCode(%q) = %q, want %q", c, got, Type(e.Slug))
			}
		}
	}
	if got := TypeForCode("no_such_code_at_all"); Registered(got) || got != Type("no-such-code-at-all") {
		t.Fatalf("an unknown code: %q", got)
	}
	for _, typ := range []string{TypeBadRequest, TypeConflict, TypeInternal, TypeUnauthorized, TypeNotFound} {
		if !Registered(typ) {
			t.Errorf("%s is not registered", typ)
		}
	}
	if Registered("about:blank") || Lookup("about:blank") != nil {
		t.Fatal("about:blank is not a registry type")
	}
}

// Every generic type a status falls back to is registered.
func TestGenericTypesAreRegistered(t *testing.T) {
	for s := 400; s <= 599; s++ {
		if typ := GenericType(s); !Registered(typ) {
			t.Errorf("GenericType(%d) = %s, not registered", s, typ)
		}
	}
	if GenericType(409) != TypeConflict || GenericType(503) != TypeUnavailable || GenericType(500) != TypeInternal || GenericType(507) != TypeInternal {
		t.Fatal("generic types")
	}
}

func get(t *testing.T) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/things/1", nil)
	r.Header.Set("Accept", "application/json")
	return r
}

// T01: in a test binary with the variable set, an unregistered type, made as a struct
// literal, is appended to the file and sent as it is.
func TestUnregisteredTypeIsRecordedInTests(t *testing.T) {
	file := filepath.Join(t.TempDir(), "unregistered.txt")
	t.Setenv(UnregisteredEnv, file)
	w := httptest.NewRecorder()
	Write(w, get(t), &Error{Type: Base + "no-such-thing", Title: "conflict", Status: http.StatusConflict})
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d", w.Code)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "no-such-thing\t409\tGET /api/things/1\n") {
		t.Fatalf("recorded %q", b)
	}
	// a registered type and about:blank are not recorded
	Write(httptest.NewRecorder(), get(t), Conflict("", "x"))
	Write(httptest.NewRecorder(), get(t), &Error{Type: "about:blank#no_update", Status: http.StatusConflict})
	if b2, _ := os.ReadFile(file); !bytes.Equal(b, b2) {
		t.Fatalf("recorded more: %q", b2)
	}
}

// T02: in production, an unregistered type keeps its status, is sent as the status's
// generic type, and is logged as problem_type_unregistered.
func TestUnregisteredTypeInProductionFallsBack(t *testing.T) {
	testMode = false
	t.Cleanup(func() { testMode = true })
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	orig := &Error{Type: Base + "no-such-thing", Title: "conflict", Status: http.StatusConflict}
	w := httptest.NewRecorder()
	Write(w, get(t), orig)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["type"] != TypeConflict {
		t.Fatalf("type %v, want %s", body["type"], TypeConflict)
	}
	if !strings.Contains(logs.String(), `"msg":"problem_type_unregistered"`) || !strings.Contains(logs.String(), `"slug":"no-such-thing"`) {
		t.Fatalf("log %s", logs.String())
	}
	if orig.Type != Base+"no-such-thing" {
		t.Fatal("the caller's error was changed")
	}

	// a 5xx falls back to internal, an empty type to its status's type
	w = httptest.NewRecorder()
	Write(w, get(t), &Error{Type: Base + "nope", Status: http.StatusInsufficientStorage})
	if w.Code != http.StatusInsufficientStorage || !strings.Contains(w.Body.String(), TypeInternal) {
		t.Fatalf("5xx: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	Write(w, get(t), &Error{Status: http.StatusServiceUnavailable})
	if !strings.Contains(w.Body.String(), TypeUnavailable) {
		t.Fatalf("empty type: %s", w.Body.String())
	}
}

// T06: the HTML page links the type's page, in Arabic for an Arabic browser.
func TestPageLinksWhatThisMeans(t *testing.T) {
	page := func(lang string, e *Error) string {
		r := httptest.NewRequest(http.MethodGet, "/api/reports/x.html", nil)
		r.Header.Set("Accept", "text/html")
		r.Header.Set("Sec-Fetch-Dest", "document")
		if lang != "" {
			r.Header.Set("Accept-Language", lang)
		}
		w := httptest.NewRecorder()
		Write(w, r, e)
		return w.Body.String()
	}
	en := page("en-GB,en;q=0.9,ar;q=0.5", NotFound("no such report"))
	if !strings.Contains(en, `href="https://maktaba.tv/problems/not-found/">What this means</a>`) {
		t.Fatalf("en page: %s", en)
	}
	ar := page("ar-SA,ar;q=0.9", NotFound("no such report"))
	if !strings.Contains(ar, `href="https://maktaba.tv/ar/problems/not-found/">ما معنى هذا</a>`) {
		t.Fatalf("ar page: %s", ar)
	}
	if blank := page("", &Error{Type: "about:blank", Title: "x", Status: http.StatusConflict}); strings.Contains(blank, "maktaba.tv/") {
		t.Fatalf("about:blank links a page: %s", blank)
	}
}

func TestPrefersArabic(t *testing.T) {
	for h, want := range map[string]bool{
		"ar": true, "ar-EG,en;q=0.8": true, "AR-sa": true, " ar ;q=1": true,
		"": false, "en,ar": false, "arn": false, "fr-FR": false,
	} {
		if got := prefersArabic(h); got != want {
			t.Errorf("prefersArabic(%q) = %v", h, got)
		}
	}
}

// T04: every code the seven runtime call sites can receive (six functions; review has two
// sites) maps through TypeForCode to a registered type, with the slug the site sent before the
// registry (its underscores as hyphens; licence_missing joins license-required). The codes were
// traced from each site's callers when the registry was seeded; a code a site gains later is
// caught by Write in that site's own tests.
var runtimeSiteCodes = map[string][]string{
	"cluster.problem": {
		"already_member", "components_unavailable", "expired", "joins_closed", "not_a_nas_home",
		"not_approved", "not_found", "not_pending", "not_removable", "pending_elsewhere",
		"roles_unsupported", "unknown_stage", "wrong_code",
	},
	"components": {
		"auth_required", "bad_bundle", "bad_request", "bad_want", "busy", "component_conflicts",
		"component_required_by", "components_unavailable", "disk_space", "download_failed",
		"hold_has_no_until", "home_only", "import_needs_tar", "import_too_large", "index_expired",
		"index_missing", "index_replayed", "index_wrong_version", "install_failed", "mixed_machines",
		"not_restartable", "not_running", "not_switchable", "offline_unknown", "offline_verify_failed",
		"offline_wrong_platform", "offline_wrong_version", "pack_missing", "pack_not_published",
		"packs_disabled", "platform_unsupported", "plugin_install_route", "required_by",
		"stale_generation", "unknown_component", "unknown_machine", "unpack_refused", "verify_failed",
	},
	"plugins/admin": {
		"auth_required", "body_not_json", "body_not_object", "body_too_large", "busy",
		"catalog_unavailable", "code_expired", "component_conflicts", "component_required_by",
		"developer_key_mismatch", "disk_space", "email_unverified", "entitlement_missing",
		"entitlements_unavailable", "field_invalid", "flow_invalid", "flow_unknown_key", "forbidden",
		"home_signature_invalid", "home_signature_replayed", "home_signature_stale", "home_unknown",
		"internal", "isolation_allow_missing", "key_invalid", "licence_missing",
		"manifest_bad_capability", "manifest_bad_depends", "manifest_bad_emit", "manifest_bad_event",
		"manifest_bad_panel", "manifest_bad_platform", "manifest_bad_range", "manifest_bad_request",
		"manifest_bad_schedule", "manifest_bad_settings", "manifest_bad_trigger",
		"manifest_duplicate_capability", "manifest_duplicate_stage", "manifest_invalid",
		"manifest_missing_file", "manifest_missing_key", "manifest_missing_string", "manifest_not_toml",
		"manifest_reserved_id", "manifest_schema_newer", "manifest_string_too_long",
		"manifest_subject_mismatch", "manifest_unknown_event", "manifest_unknown_key",
		"method_not_allowed", "not_for_sale", "not_found", "package_bad_entry", "package_bad_head",
		"package_bad_path", "package_digest_mismatch", "package_missing_file", "package_too_large",
		"package_too_many_files", "package_unlisted_file", "paid_downloads_unavailable",
		"payments_unavailable", "plugin_api_newer", "plugin_bad_manifest", "plugin_bad_path",
		"plugin_compile", "plugin_compile_timeout", "plugin_data_newer", "plugin_delisted",
		"plugin_download_failed", "plugin_flow_cycle", "plugin_flow_invalid", "plugin_forbidden_import",
		"plugin_install_failed", "plugin_install_form", "plugin_invalid", "plugin_link",
		"plugin_missing_export", "plugin_name_mismatch", "plugin_no_previous", "plugin_not_component",
		"plugin_not_in_catalog", "plugin_not_installed", "plugin_preload_unavailable",
		"plugin_required_capability", "plugin_revoked", "plugin_signature_invalid",
		"plugin_trusted_field", "plugin_undeclared_import", "plugin_unknown_capability",
		"plugin_unknown_library", "plugin_unreviewed_disallowed", "plugin_wrong_package",
		"plugins_install_off", "plugins_unavailable", "rate_limited", "rating_no_home",
		"rating_too_early", "reason_invalid", "reauth_required", "settings_invalid",
		"settings_schema_invalid", "settings_secret_forbidden", "signed_out", "store_link_not_started",
		"store_list_stale", "store_not_linked", "store_offline", "store_unavailable",
		"store_unreachable", "try_again", "unknown_component", "version_not_found",
	},
	"review.writeEditErr": {
		"already_undone", "changed_since", "not_active", "not_found", "stale",
	},
	"rules.conflict": {
		"settings_need_migration", "settings_newer_schema", "settings_replace_required",
		"settings_schema_changed",
	},
	"tvremote.writeTuneError": {
		"attachment_off", "budget_exhausted", "cloudflare_error", "destination_in_use",
		"invalid_attachment", "invalid_destination", "invalid_rules", "invalid_site_url", "invalid_slug",
		"no_such_attachment", "no_such_channel", "no_such_destination", "no_such_session",
		"remote_channels_full", "remote_failed", "remote_not_allowed", "remote_storage_full",
		"remote_unavailable", "rights_confirmation_required", "session_ended", "slug_taken",
		"too_many_outputs", "user_streams_full",
	},
}

func TestRuntimeSiteCodesAreRegistered(t *testing.T) {
	if len(runtimeSiteCodes) != 6 {
		t.Fatalf("%d sites, want the six functions", len(runtimeSiteCodes))
	}
	for site, codes := range runtimeSiteCodes {
		for _, code := range codes {
			typ := TypeForCode(code)
			if !Registered(typ) {
				t.Errorf("%s: code %q is not in any entry's codes", site, code)
				continue
			}
			want := strings.ReplaceAll(code, "_", "-")
			if code == "licence_missing" {
				want = "license-required"
			}
			if typ != Type(want) {
				t.Errorf("%s: code %q maps to %s, want %s", site, code, typ, Type(want))
			}
		}
	}
}
