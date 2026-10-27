package browse

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Many PUTs of the same user's pins at once on Postgres all succeed (each
// waits for the one before), and the pins are one of the lists sent. Runs
// only with MAKTABA_TEST_PG_URL.
func TestPutPinsConcurrentPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	for _, q := range []string{
		`INSERT INTO users (id, username, pw_hash) VALUES ('` + viewer + `', 'pins-viewer', 'x')`,
		`INSERT INTO libraries (id, name, roots) VALUES ('` + libA + `', 'Alpha', '{/m/a}'), ('` + libB + `', 'Beta', '{/m/b}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	r := chi.NewRouter()
	(&Handler{DB: db, NowFunc: func() time.Time { return now }}).Mount(r)
	p := &principal.Principal{UserID: viewer, Libraries: []string{libA, libB}}
	bodies := []string{`{"library_ids":["` + libA + `","` + libB + `"]}`, `{"library_ids":["` + libB + `","` + libA + `"]}`}

	var wg sync.WaitGroup
	codes := make(chan string, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("PUT", "/api/me/pinned-libraries", bytes.NewReader([]byte(bodies[i%2])))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				codes <- rec.Body.String()
			}
		}()
	}
	wg.Wait()
	close(codes)
	for body := range codes {
		t.Errorf("a PUT failed: %s", body)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_library_pins WHERE user_id = $1`, viewer).Scan(&n); err != nil || n != 2 {
		t.Errorf("%d pins (%v), want 2", n, err)
	}
}
