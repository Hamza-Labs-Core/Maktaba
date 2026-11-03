package router

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	componentsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws/eventbus"
	catalog "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store/pqlisten"
)

// replica is one API process's share of story 51.09: its hub, bus, capabilities and the
// two routes, behind a viewer's sign-in.
type replica struct {
	srv  *httptest.Server
	caps *components.Capabilities
}

func startReplica(ctx context.Context, t *testing.T, db *sql.DB, st *store.Store, dsn string) *replica {
	t.Helper()
	hub := ws.NewHub()
	bus := eventbus.New(eventbus.NewPostgresBackend(db, dsn, slog.Default()), hub, slog.Default())
	go func() { _ = bus.Run(ctx) }()
	caps := components.NewCapabilities(components.CapabilitiesOptions{})
	l, err := pqlisten.New(dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	go caps.Run(ctx, st, l, CapabilitiesPublisher(db, bus, hub))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "viewer"})))
		})
	})
	(&ws.Handler{Hub: hub, Replay: busReplayAdapter{bus: bus}}).Mount(r)
	(&componentsh.Capabilities{Caps: caps}).Mount(r)
	srv := httptest.NewServer(r)
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	return &replica{srv: srv, caps: caps}
}

// homeEvents reads /ws/home's frames of one client into a channel.
func homeEvents(ctx context.Context, t *testing.T, url string) <-chan ws.Event {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/ws/home", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan ws.Event, 16)
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var e ws.Event
				if json.Unmarshal([]byte(data), &e) == nil {
					out <- e
				}
			}
		}
	}()
	return out
}

// T07 (story 51.09): two API replicas on one database, a viewer's /ws/home on each: an
// admin's change on replica A reaches both within 2 s as one capabilities_changed with
// the new ETag, and the next fetch shows youtube off.
func TestPGCapabilitiesPushReachesEveryReplica(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	st := components.New(db)
	bg := context.Background()
	if _, err := st.SetDesired(bg, store.Desired{ID: "youtube", Want: catalog.WantEnabled},
		store.Audit{By: "test", ActorSource: "test", Action: "component.enable"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	a := startReplica(ctx, t, db, st, dsn)
	b := startReplica(ctx, t, db, st, dsn)
	// both have read the row and pushed their first answer (once: the same ETag)
	deadline := time.Now().Add(5 * time.Second)
	for a.caps.Snapshot().ETag() == "c-0-1" || b.caps.Snapshot().ETag() != a.caps.Snapshot().ETag() {
		if time.Now().After(deadline) {
			t.Fatalf("replicas never loaded: %s %s", a.caps.Snapshot().ETag(), b.caps.Snapshot().ETag())
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(components.CoalesceWindow + 500*time.Millisecond)
	evA, evB := homeEvents(ctx, t, a.srv.URL), homeEvents(ctx, t, b.srv.URL)
	time.Sleep(200 * time.Millisecond) // both streams are open

	if _, err := st.SetDesired(bg, store.Desired{ID: "youtube", Want: catalog.WantInstalled, Generation: 1},
		store.Audit{By: "admin", ActorSource: "web", Action: "component.disable"}); err != nil {
		t.Fatal(err)
	}
	for name, ev := range map[string]<-chan ws.Event{"A": evA, "B": evB} {
		select {
		case e := <-ev:
			want := a.caps.Snapshot().ETag()
			if e.Type != ws.EventCapabilitiesChanged || e.Payload["etag"] != want {
				t.Fatalf("%s: %+v, want etag %s", name, e, want)
			}
		case <-time.After(2500 * time.Millisecond):
			t.Fatalf("%s: no capabilities_changed", name)
		}
	}
	// one push for one change, however many replicas saw it
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE channel = 'home' AND type = 'capabilities_changed'
		AND payload->>'etag' = $1`, a.caps.Snapshot().ETag()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d events for one change", n)
	}
	resp, err := http.Get(b.srv.URL + "/api/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Features map[string]string `json:"features"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Features["youtube"] != "off" {
		t.Fatalf("youtube %q after the change", body.Features["youtube"])
	}
}
