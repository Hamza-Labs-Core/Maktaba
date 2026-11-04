package components

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
	comps "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.19 T08 on a real Postgres, the API's publisher and its runner: a licence whose
// grace ends stops the cloudlink reporter on the runner's own clock (Waiting,
// licence_missing, the want kept), with no write of the desired state; a licence
// applied again starts it within 30 s, with no admin action; cloud.link keeps running.
func TestLicenceLapseAndReturn(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	t.Setenv("MAKTABA_COMPONENTS_RECONCILE", "")
	ctx := context.Background()
	st := New(db)
	for _, id := range []string{"cloud.link", "cloud.relay"} {
		if _, err := st.SetDesired(ctx, store.Desired{ID: id, Want: comps.WantEnabled},
			store.Audit{By: store.SeedBy, ActorSource: "seed", Action: "component.seed"}); err != nil {
			t.Fatal(err)
		}
	}
	// a Home licence whose grace (expires_at + SkewGrace) ends in 3 s
	lic := subscriptions.NewStore()
	home := func(end time.Time) *subscriptions.Entitlements {
		return &subscriptions.Entitlements{Tier: subscriptions.TierHome, LicenseID: "l", ExpiresAt: end.Add(-subscriptions.SkewGrace),
			Features: map[subscriptions.Feature]bool{subscriptions.FeatureCloudRelay: true}}
	}
	lic.Set(home(time.Now().Add(3 * time.Second)))
	svc := &Service{Store: st, Catalog: comps.Default(), Logger: keptLog(t)}
	lic.OnChange(func() {
		if _, err := svc.PublishLicences(ctx, lic); err != nil {
			t.Error(err)
		}
	})
	if _, err := svc.PublishLicences(ctx, lic); err != nil {
		t.Fatal(err)
	}
	genBefore := wantOfRow(t, st).Generation

	r := NewRunner(keptLog(t))
	var starts, stops atomic.Int32
	r.Declare("cloudlink_reporter", func(ctx context.Context) error {
		starts.Add(1)
		<-ctx.Done()
		stops.Add(1)
		return nil
	})
	Start(r, db, dsn, keptLog(t))
	status := func() store.Status {
		rows, err := st.StatusOf(ctx, "cloud.relay")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range rows {
			if s.Machine == store.Home && s.Part == Host {
				return s
			}
		}
		return store.Status{}
	}
	within := func(d time.Duration, what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(d)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("%s: not within %s", what, d)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	// running as the loaded desired state says (its generation): the runner's first report
	// can go out before its first load, with the catalog's defaults (generation 0) and no
	// licence read yet (an integration run asked Licensed then: true, not known)
	within(5*time.Second, "running", func() bool {
		s := status()
		return s.Phase == comps.PhaseRunning && s.ObservedGeneration == genBefore
	})
	if lic, known := r.Licensed("cloud.relay"); !lic || !known {
		t.Fatalf("Licensed %v %v while granted", lic, known)
	}
	rev, _ := st.Revision(ctx)

	// the grace ends: nobody writes, the runner stops the reporter itself
	within(10*time.Second, "the reporter stopped", func() bool { return stops.Load() == 1 })
	within(5*time.Second, "waiting", func() bool {
		s := status()
		return s.Phase == comps.PhaseWaiting && s.Reason() == "licence_missing"
	})
	if now, _ := st.Revision(ctx); now != rev {
		t.Fatalf("the desired state was written while the licence lapsed: revision %d -> %d", rev, now)
	}
	if d := wantOfRow(t, st); d.Want != comps.WantEnabled || d.Generation != genBefore {
		t.Fatalf("the want moved: %+v", d)
	}
	if !r.On("cloud.link") {
		t.Fatal("cloud.link stopped with the licence")
	}
	if lic, known := r.Licensed("cloud.relay"); lic || !known {
		t.Fatalf("Licensed %v %v after the grace", lic, known)
	}

	// a valid licence applied (a refresher's apply): running again, no admin action
	applied := time.Now()
	lic.Set(home(time.Now().Add(31 * 24 * time.Hour)))
	within(30*time.Second, "running again", func() bool { return starts.Load() == 2 && status().Phase == comps.PhaseRunning })
	t.Logf("running again %s after the licence", time.Since(applied).Round(time.Millisecond))
}

func wantOfRow(t *testing.T, st *store.Store) store.Desired {
	t.Helper()
	d, err := st.DesiredOf(context.Background(), "cloud.relay")
	if err != nil {
		t.Fatal(err)
	}
	return d
}
