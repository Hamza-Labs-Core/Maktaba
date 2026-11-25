package router

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
)

// The scheduler's hold on a stale list follows the cadence the home's checks run at
// (MAKTABA_UPDATE_INTERVAL_SEC).
func TestTheSchedulerKnowsTheCadence(t *testing.T) {
	for v, want := range map[string]time.Duration{"": updates.CheckInterval, "86400": 24 * time.Hour, "0": 0, "often": updates.CheckInterval} {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		h := MountP35(chi.NewRouter(), P35Deps{DB: db, HTTPClient: http.DefaultClient, Getenv: func(k string) string {
			if k == "MAKTABA_UPDATE_INTERVAL_SEC" {
				return v
			}
			return ""
		}})
		if h == nil || h.Svc.CheckEvery == nil || *h.Svc.CheckEvery != want {
			t.Errorf("%q: %+v, want %s", v, h, want)
		}
		_ = db.Close()
	}
}
