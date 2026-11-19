package subscriptions

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Story 16.4, the owner's decision of 2026-11-19: past the seats, admins are exempt and
// the newest accounts beyond the seat count go read-only; they watch and keep their
// progress and history, nothing else; removing users or adding seats restores them at
// once.

func seatDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "seats.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL,
		pw_hash TEXT NOT NULL, is_admin INTEGER NOT NULL DEFAULT 0, created_at TIMESTAMP NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func addUser(t *testing.T, db *sql.DB, id string, admin bool, at time.Time) {
	t.Helper()
	a := 0
	if admin {
		a = 1
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin, created_at) VALUES ($1, $2, 'h', $3, $4)`,
		id, "user-"+id, a, at.UTC()); err != nil {
		t.Fatal(err)
	}
}

func homeStore(t *testing.T, seats int) *Store {
	t.Helper()
	s := NewStore()
	now := time.Now().UTC()
	s.Set(&Entitlements{Tier: TierHome, LicenseID: "home", Seats: seats, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour),
		Features: map[Feature]bool{FeatureMultiUser: true}})
	return s
}

func ids(us []SeatUser) []string {
	out := []string{}
	for _, u := range us {
		out = append(out, u.ID)
	}
	return out
}

func TestTheNewestAccountsBeyondTheSeatsAreReadOnly(t *testing.T) {
	ctx := context.Background()
	db := seatDB(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addUser(t, db, "owner", true, base)
	for i := 1; i <= 5; i++ { // a, b, c, d, e: e the newest
		addUser(t, db, string(rune('a'+i-1)), false, base.Add(time.Duration(i)*time.Hour))
	}
	// an admin made last is still exempt
	addUser(t, db, "late-admin", true, base.Add(10*time.Hour))
	// the disabled sentinel row is no seat
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin, created_at) VALUES ('sentinel', 's', $1, 1, $2)`,
		disabledHash, base.Add(20*time.Hour)); err != nil {
		t.Fatal(err)
	}
	g := &SeatGuard{DB: db, Store: homeStore(t, 4)} // 7 accounts, 4 seats: 3 read-only
	got, err := g.ReadOnlyUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(got)) != "[e d c]" {
		t.Fatalf("read-only = %v, want the three newest non-admins [e d c]", ids(got))
	}
	for id, want := range map[string]bool{"e": true, "c": true, "b": false, "owner": false, "late-admin": false} {
		if ro, _ := g.ReadOnly(ctx, id); ro != want {
			t.Errorf("ReadOnly(%s) = %v, want %v", id, ro, want)
		}
	}

	// removing a user restores the oldest read-only one at once
	if _, err := db.Exec(`DELETE FROM users WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.ReadOnlyUsers(ctx); fmt.Sprint(ids(got)) != "[e d]" {
		t.Fatalf("after a removal: %v", ids(got))
	}
	// more seats: none
	g.Store = homeStore(t, 0) // Home's own 4 → still over? 6 accounts, 4 seats: 2
	if got, _ := g.ReadOnlyUsers(ctx); len(got) != 2 {
		t.Fatalf("Home's 4 seats for 6 accounts: %v", ids(got))
	}
	pro := NewStore()
	pro.Set(&Entitlements{Tier: TierPro, LicenseID: "pro", Features: map[Feature]bool{FeatureMultiUser: true}})
	g.Store = pro
	if got, _ := g.ReadOnlyUsers(ctx); len(got) != 0 {
		t.Fatalf("Pro (unlimited): %v", ids(got))
	}
}

func TestWhenAdminsAloneFillTheSeatsEveryOtherAccountIsReadOnly(t *testing.T) {
	db := seatDB(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addUser(t, db, "x", false, base)
	addUser(t, db, "admin1", true, base.Add(time.Hour))
	addUser(t, db, "admin2", true, base.Add(2*time.Hour))
	g := &SeatGuard{DB: db, Store: NewStore()} // Free: 1 seat; 3 accounts: 2 over, one non-admin
	got, _ := g.ReadOnlyUsers(context.Background())
	if fmt.Sprint(ids(got)) != "[x]" {
		t.Fatalf("read-only = %v, want [x] (admins exempt)", ids(got))
	}
}

func TestTheGateLetsAReadOnlyAccountWatchOnly(t *testing.T) {
	db := seatDB(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addUser(t, db, "owner", true, base)
	addUser(t, db, "old", false, base.Add(time.Hour))
	addUser(t, db, "new", false, base.Add(2*time.Hour))
	g := &SeatGuard{DB: db, Store: NewStore()} // Free, 1 seat: old and new read-only? 3-1=2: new, old
	h := g.Gate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	call := func(user string, admin bool, method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		if user != "" {
			req = req.WithContext(principal.WithPrincipal(req.Context(),
				&principal.Principal{UserID: user, IsAdmin: admin, Source: principal.SourceCookie}))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	allowed := [][2]string{
		{http.MethodGet, "/api/videos/v1"},
		{http.MethodPost, "/api/stream/sessions"},
		{http.MethodPost, "/api/stream/sessions/s1/progress"},
		{http.MethodDelete, "/api/stream/sessions/s1"},
		{http.MethodPost, "/api/watch/start"},
		{http.MethodPost, "/api/watch/heartbeat"},
		{http.MethodPost, "/api/me/watched"},
		{http.MethodDelete, "/api/me/history/v1"},
		{http.MethodPost, "/api/me/play-queues"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodPost, "/api/me/change-password"},
		{http.MethodPost, "/api/search"},
		{http.MethodPost, "/api/channels/c1/play"},
		{http.MethodPost, "/api/me/parental/requests"},
	}
	refused := [][2]string{
		{http.MethodPost, "/api/libraries/l1/scan"},
		{http.MethodPatch, "/api/videos/v1"},
		{http.MethodPost, "/api/videos/v1/transcript/words"},
		{http.MethodPost, "/api/collections"},
		{http.MethodPost, "/api/me/playlists"},
		{http.MethodPost, "/api/me/favorites"},
		{http.MethodPost, "/api/users"},
		{http.MethodPatch, "/api/settings"},
		{http.MethodPost, "/api/channels"},
		{http.MethodPost, "/api/channels/c1/regenerate"},
		{http.MethodPost, "/api/search/save"},
	}
	for _, c := range allowed {
		if code := call("new", false, c[0], c[1]); code != http.StatusNoContent {
			t.Errorf("read-only %s %s = %d, want it allowed", c[0], c[1], code)
		}
	}
	for _, c := range refused {
		if code := call("new", false, c[0], c[1]); code != http.StatusForbidden {
			t.Errorf("read-only %s %s = %d, want 403", c[0], c[1], code)
		}
		if code := call("owner", true, c[0], c[1]); code != http.StatusNoContent {
			t.Errorf("the admin %s %s = %d", c[0], c[1], code)
		}
	}
	// no account: the gate is not the auth gate
	if code := call("", false, http.MethodPost, "/api/libraries/l1/scan"); code != http.StatusNoContent {
		t.Errorf("no principal: %d", code)
	}
	// a seat added (Home: 4) and both work again at once
	g.Store = homeStore(t, 0)
	if code := call("new", false, http.MethodPost, "/api/me/playlists"); code != http.StatusNoContent {
		t.Errorf("after seats were added: %d", code)
	}
	// the body names the problem
	g.Store = NewStore()
	req := httptest.NewRequest(http.MethodPost, "/api/collections", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "new", Source: principal.SourceCookie}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if body := rec.Body.String(); rec.Code != http.StatusForbidden || !strings.Contains(body, "seat-read-only") || !strings.Contains(body, "Ask the home's admin") {
		t.Fatalf("refusal = %d %s", rec.Code, body)
	}
}

func TestTheGateLetsAWriteThroughWhenTheUsersCannotBeRead(t *testing.T) {
	db := seatDB(t)
	_ = db.Close()
	g := &SeatGuard{DB: db, Store: NewStore()}
	h := g.Gate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodPost, "/api/collections", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u", Source: principal.SourceCookie}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a database error refused the write: %d", rec.Code)
	}
	var nilGuard *SeatGuard
	rec = httptest.NewRecorder()
	nilGuard.Gate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("no guard: %d", rec.Code)
	}
}
