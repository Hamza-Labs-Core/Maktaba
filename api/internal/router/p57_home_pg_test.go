package router_test

// Epic 57 story 57.13: the whole home behind the production router, for the role table
// (p57_roles_pg_test.go) and the bypass rehearsal (p57_bypass_pg_test.go). It is
// setupFilter's home (two libraries, videos at all ages, 16+ and unrated, a series and a
// collection that are 16+ only, a 9+ child with a guardian, a second child with another)
// with every other Epic 57 mount on the same router: live TV with a 9+ and a 16+ channel,
// the age tiers, the review list, ask a parent and the PIN, the capabilities.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/signedurl"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const (
	phCh9       = "00000000-0000-4000-8000-0000573e0009" // a 9+ channel: a 9+ programme now, a 16+ one next
	phCh16      = "00000000-0000-4000-8000-0000573e0016" // a 16+ channel
	phRule      = "00000000-0000-4000-8000-0000573f0001" // the child's block rule on the unrated video
	phRequest   = "00000000-0000-4000-8000-0000573f0002" // the child's pending request for the teen video
	phFamily    = "00000000-0000-4000-8000-0000573f0003" // the child's app sign-in, with a device signal
	phSource    = "00000000-0000-4000-8000-0000573f0004" // a YouTube source of library A
	phCh16Name  = "Zarqa-Channel-Sixteen"
	phProgramme = "Zarqa-Programme-Secret"
)

type p57Home struct {
	*pfEnv
	// routes are the home's routes as "METHOD pattern", sorted
	routes []string
	notes  *notify.Hub
}

func setupHome(t testing.TB) *p57Home {
	t.Helper()
	h := &p57Home{}
	h.pfEnv = setupFilterWith(t, func(r chi.Router, e *pfEnv) {
		e.cache = e.gate.Cache()
		key, _, err := signedurl.LoadOrCreateKey(filepath.Join(t.TempDir(), "stream.key"))
		if err != nil {
			t.Fatal(err)
		}
		router.MountP27(r, router.P27Deps{DB: e.db, DataDir: t.TempDir(), ChannelSigner: signedurl.FromKey(key), Parental: e.cache})
		rc := router.MountP57Tiers(r, router.P57TiersDeps{DB: e.db})
		if rc == nil {
			t.Fatal("MountP57Tiers answered no recompute")
		}
		router.MountP57Review(r, router.P57ReviewDeps{DB: e.db, Recompute: rc})
		h.notes = notify.NewHub(e.db, nil)
		router.MountP57Requests(r, router.P57RequestsDeps{DB: e.db, Cache: e.cache, Notifier: h.notes})
		router.MountP29(r, router.P29Deps{DB: e.db}) // watching: start, heartbeat, stop
		router.MountP51(r, router.P51Deps{DB: e.db, Parental: router.ParentalCapabilities(e.cache)})
		seen := map[string]bool{}
		_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if k := method + " " + route; !seen[k] {
				seen[k] = true
				h.routes = append(h.routes, k)
			}
			return nil
		})
		sort.Strings(h.routes)
		// the tiers the fixtures were given stay as given: the migration's own recompute
		// job would rate them again from nothing
		e.exec(`DELETE FROM parental_recompute_jobs`)
		_, _ = rc.Drain(context.Background())
	})
	e := h.pfEnv
	e.exec(`INSERT INTO channels (id, number, name, slug, mode, age_tier) VALUES
		($1, 901, 'Nine', 'nine', 'shuffle', 9), ($2, 916, $3, 'sixteen', 'shuffle', 16)`, phCh9, phCh16, phCh16Name)
	now := time.Now().UTC().Truncate(time.Second)
	for i, p := range []struct {
		ch, title string
		from, to  time.Duration
		tier      any
	}{
		{phCh9, "Open nine", -30 * time.Minute, 30 * time.Minute, 9},
		{phCh9, phProgramme, 30 * time.Minute, 90 * time.Minute, 16},
		{phCh16, phProgramme + " on sixteen", -30 * time.Minute, time.Hour, nil},
	} {
		e.exec(`INSERT INTO channel_programs (channel_id, seq, kind, start_at, end_at, source_duration, title_snapshot, age_tier, sched_gen)
			VALUES ($1, $2, 'slate', $3, $4, 3600000, jsonb_build_object('title', $5::text), $6, tv_live_gen())`,
			p.ch, i, now.Add(p.from), now.Add(p.to), p.title, p.tier)
	}
	e.exec(`INSERT INTO youtube_sources (id, library_id, kind, url, external_id) VALUES ($1, $2, 'channel', 'https://example.invalid/c', 'UCpf')`, phSource, pfLibA)
	e.exec(`INSERT INTO refresh_tokens (id, user_id, hash, family_id, expires_at) VALUES (gen_random_uuid(), $1, 'h-ph', $2, now() + interval '30 days')`, pfChild, phFamily)
	h.reset()
	return h
}

// reset puts back what a role row may have changed: the two children at their limits with
// their guardians, the child's one rule, its one pending request, parental controls on.
func (h *p57Home) reset() {
	h.t.Helper()
	h.exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1), ($2, 9, 1)
		ON CONFLICT (user_id) DO UPDATE SET max_tier = 9, unrated = 'hide', allow_only = false, version = 1`, pfChild, pfChild2)
	h.exec(`DELETE FROM parental_guardians WHERE NOT ((child_id = $1 AND guardian_id = $2) OR (child_id = $3 AND guardian_id = $4))`,
		pfChild, pfGuardian, pfChild2, pfOtherG)
	h.exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1,$2), ($3,$4) ON CONFLICT DO NOTHING`, pfChild, pfGuardian, pfChild2, pfOtherG)
	h.exec(`DELETE FROM parental_rules WHERE id <> $1`, phRule)
	h.exec(`INSERT INTO parental_rules (id, user_id, effect, subject_kind, subject_id) VALUES ($1, $2, 'block', 'video', $3) ON CONFLICT DO NOTHING`,
		phRule, pfChild, pfUnrated)
	h.exec(`DELETE FROM parental_requests WHERE id <> $1`, phRequest)
	h.exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ($1, $2, 'video', $3)
		ON CONFLICT (id) DO UPDATE SET status = 'pending', decided_by = NULL, decided_at = NULL`, phRequest, pfChild, pfTeen)
	h.exec(`INSERT INTO session_age_signals (refresh_family_id, user_id, upper, tier_limit, observed_at) VALUES ($1, $2, 12, 9, now())
		ON CONFLICT (refresh_family_id) DO UPDATE SET tier_limit = 9, pending_looser = NULL`, phFamily, pfChild)
	h.exec(`DELETE FROM parental_time_rules`)
	h.exec(`UPDATE app_settings SET value = 'true' WHERE key = 'parental.enabled'`)
	h.exec(`UPDATE users SET is_admin = (id = $1)`, pfAdmin)
	h.cache.Invalidate("")
}

// rawWith is raw with one more header.
func (h *p57Home) rawWith(who, method, path, header, value string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Test-As", who)
	req.Header.Set(header, value)
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

// withFamily answers userID's requests as its app sign-in family.
func withFamily(userID, family string) func(*http.Request, *principal.Principal) *principal.Principal {
	return func(_ *http.Request, p *principal.Principal) *principal.Principal {
		if p != nil && p.UserID == userID {
			q := *p
			q.RefreshFamily = family
			return &q
		}
		return p
	}
}

// withLibraries answers userID's requests with only these libraries.
func withLibraries(userID string, libs ...string) func(*http.Request, *principal.Principal) *principal.Principal {
	return func(_ *http.Request, p *principal.Principal) *principal.Principal {
		if p != nil && p.UserID == userID {
			q := *p
			q.Libraries = libs
			return &q
		}
		return p
	}
}
