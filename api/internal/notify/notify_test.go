package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

const (
	alice = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" // admin
	bob   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" // admin
	carol = "cccccccc-cccc-4ccc-8ccc-cccccccccccc" // not an admin
)

func call(t *testing.T, r http.Handler, method, path, user string, isAdmin bool) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: user, IsAdmin: isAdmin}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// TC4 (story 33.13): a subscriber joins the hub without the producer changing; every
// subscriber gets the notice, and one failing does not stop the others.
func TestNotifySubscriberInterface(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			hub := notify.NewHub(be.DB, nil)
			var pushed []notify.Notification
			hub.Subscribe(notify.SubscriberFunc(func(context.Context, notify.Notification) error {
				return errors.New("push gateway down")
			}))
			hub.Subscribe(notify.SubscriberFunc(func(_ context.Context, n notify.Notification) error {
				pushed = append(pushed, n)
				return nil
			}))
			var producer notify.Notifier = hub
			err := producer.Notify(context.Background(), "tv.change.live", notify.Payload{
				Severity: notify.SeveritySuccess, Params: map[string]any{"number": 42}, Link: "/admin/live-tv/changes/x"})
			if err == nil {
				t.Fatal("the failing subscriber's error is returned")
			}
			if len(pushed) != 1 || pushed[0].Kind != "tv.change.live" || pushed[0].TitleKey != "notify.tv.change.live" ||
				pushed[0].Audience != notify.AudienceAdmins {
				t.Fatalf("pushed %+v", pushed)
			}
			items, err := (&notify.Store{DB: be.DB}).List(context.Background(), alice, true, 10, false)
			if err != nil || len(items) != 1 || items[0].Params["number"] != float64(42) {
				t.Fatalf("stored %+v %v", items, err)
			}
		})
	}
}

// AC2: the bell's API - unread count, list, read per user, admins' notices only for
// admins, a user's own notices for that user.
func TestNotificationsAPI(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			hub := notify.NewHub(be.DB, nil)
			now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
			hub.Now = func() time.Time { return now }
			ctx := context.Background()
			for i, kind := range []string{"tv.change.ready", "tv.change.failed", "tv.change.live"} {
				now = now.Add(time.Minute)
				if err := hub.Notify(ctx, kind, notify.Payload{Subject: kind, Params: map[string]any{"i": i}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := hub.Notify(ctx, "export.done", notify.Payload{Audience: carol}); err != nil {
				t.Fatal(err)
			}
			r := chi.NewRouter()
			(&notify.Handler{Store: &notify.Store{DB: be.DB}}).Mount(r)

			code, out := call(t, r, "GET", "/api/notifications", alice, true)
			items, _ := out["items"].([]any)
			if code != 200 || out["unread"] != float64(3) || len(items) != 3 {
				t.Fatalf("alice %d %v", code, out)
			}
			first := items[0].(map[string]any)
			if first["kind"] != "tv.change.live" || first["read"] != false {
				t.Fatalf("newest first %v", first)
			}
			code, out = call(t, r, "GET", "/api/notifications", carol, false)
			if items, _ := out["items"].([]any); code != 200 || len(items) != 1 || out["unread"] != float64(1) {
				t.Fatalf("carol sees only hers: %d %v", code, out)
			}
			// carol cannot mark an admins' notice
			if code, _ := call(t, r, "POST", "/api/notifications/"+first["id"].(string)+"/read", carol, false); code != 404 {
				t.Fatalf("carol marks an admins' notice: %d", code)
			}
			code, out = call(t, r, "POST", "/api/notifications/"+first["id"].(string)+"/read", alice, true)
			if code != 200 || out["unread"] != float64(2) {
				t.Fatalf("read %d %v", code, out)
			}
			// again: idempotent
			if code, out = call(t, r, "POST", "/api/notifications/"+first["id"].(string)+"/read", alice, true); code != 200 || out["unread"] != float64(2) {
				t.Fatalf("read again %d %v", code, out)
			}
			// read is per user: bob still has three
			if _, out = call(t, r, "GET", "/api/notifications?unread=1", bob, true); out["unread"] != float64(3) {
				t.Fatalf("bob %v", out)
			}
			if code, out = call(t, r, "POST", "/api/notifications/read-all", alice, true); code != 200 || out["unread"] != float64(0) {
				t.Fatalf("read all %d %v", code, out)
			}
			if _, out = call(t, r, "GET", "/api/notifications?unread=1", alice, true); len(out["items"].([]any)) != 0 {
				t.Fatalf("unread list after read-all %v", out)
			}
			if code, _ := call(t, r, "POST", "/api/notifications/not-a-uuid/read", alice, true); code != 404 {
				t.Fatalf("bad id %d", code)
			}
			if code, _ := call(t, r, "GET", "/api/notifications", "", false); code != 403 {
				t.Fatalf("anonymous %d", code)
			}
		})
	}
}
