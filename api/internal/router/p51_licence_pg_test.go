package router

import (
	"context"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store/pqlisten"
)

// Story 51.19 T07 on Postgres: the components' routes publish what the licence allows
// when they are mounted, and again on every change of the licence store (a refresher's
// apply is SetLicense; a revocation is Lock): the row is rewritten and NOTIFY
// components_changed is sent once per change, the generation untouched.
func TestP51PublishesTheLicence(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	ctx := context.Background()
	st := store.New(db)
	if _, err := st.SetDesired(ctx, store.Desired{ID: "cloud.relay", Want: components.WantEnabled},
		store.Audit{By: store.SeedBy, ActorSource: "seed", Action: "component.seed"}); err != nil {
		t.Fatal(err)
	}
	row := func() store.Desired {
		t.Helper()
		d, _, err := st.Desired(ctx, store.Home)
		if err != nil {
			t.Fatal(err)
		}
		return d["cloud.relay"]
	}
	gen := row().Generation

	lic := subscriptions.NewStore()
	ref := &subscriptions.StoreRef{}
	ref.Set(lic)
	if MountP51(chi.NewRouter(), P51Deps{DB: db, License: ref}) == nil {
		t.Fatal("not mounted")
	}
	if l := row().Licence; l == nil || l.Licensed {
		t.Fatalf("free, published at mount: %+v", l)
	}

	l, err := pqlisten.New(dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	drain := func() {
		for {
			select {
			case <-l.Events():
			case <-time.After(300 * time.Millisecond):
				return
			}
		}
	}
	drain() // the LISTEN is in place, the reconnect's Reload is read
	count := func() int {
		n := 0
		for {
			select {
			case ev := <-l.Events():
				if !ev.Reload && ev.ID == "cloud.relay" {
					n++
				}
			case <-time.After(500 * time.Millisecond):
				return n
			}
		}
	}

	exp := time.Now().Add(31 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	lic.Set(&subscriptions.Entitlements{Tier: subscriptions.TierHome, LicenseID: "l1", ExpiresAt: exp,
		Features: map[subscriptions.Feature]bool{subscriptions.FeatureCloudRelay: true}})
	if n := count(); n != 1 {
		t.Fatalf("%d notifications for one licence change, want 1", n)
	}
	d := row()
	if d.Licence == nil || !d.Licence.Licensed || d.Licence.Until == nil || !d.Licence.Until.Equal(exp.Add(subscriptions.SkewGrace)) {
		t.Fatalf("licensed row %+v", d.Licence)
	}
	if d.Generation != gen || d.Want != components.WantEnabled {
		t.Fatalf("the wish moved: %+v (generation was %d)", d, gen)
	}

	// a change of the store that changes nothing the rows say: no notification
	if err := lic.RecordRefresh(ctx, nil); err != nil {
		t.Fatal(err)
	}
	lic.Set(lic.Current())
	if n := count(); n != 0 {
		t.Fatalf("%d notifications for no change", n)
	}

	if err := lic.Lock(ctx, subscriptions.LockRevoked); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("%d notifications for a revocation, want 1", n)
	}
	if d := row(); d.Licence == nil || d.Licence.Licensed || d.Licence.Until != nil || d.Want != components.WantEnabled {
		t.Fatalf("revoked row %+v", d)
	}
}
