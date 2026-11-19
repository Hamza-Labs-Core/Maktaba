package components

import (
	"context"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Licence gating (story 51.19). A component can name one licence flag (today only
// cloud.relay, by cloud_relay). The service refuses to turn it on without it; the API,
// the one writer of desired state, publishes on its desired row what the licence allows
// (licensed, licensed_until), and every runner reads that with its own clock: a lapsed
// licence makes it wait, its want kept. The licence store's grace is the only grace.

// Licences is what lic allows now of every component of cat that names a licence flag:
// licensed when the effective licence grants the flag, until the latest end of its grant
// (an applied licence's expires_at + Grace, a replaced one's end + Grace: story 16.2);
// none for a licence that does not expire, or one not granted.
func Licences(cat *components.Catalog, lic *subscriptions.Store) map[string]store.Licence {
	out := map[string]store.Licence{}
	for _, c := range cat.Components() {
		if c.Licence == "" {
			continue
		}
		until, ok := lic.Until(subscriptions.Feature(c.Licence))
		l := store.Licence{Licensed: ok}
		if ok && !until.IsZero() {
			u := until.UTC()
			l.Until = &u
		}
		out[c.ID] = l
	}
	return out
}

// PublishLicences writes what lic allows on the desired rows (only those that change: one
// NOTIFY components_changed per changed row) and answers how many changed.
func (s *Service) PublishLicences(ctx context.Context, lic *subscriptions.Store) (int, error) {
	n, err := s.Store.SetLicences(ctx, Licences(s.cat(), lic))
	if err != nil {
		return n, err
	}
	if n > 0 {
		s.log().Info("components: published what the licence allows", "rows", n, "tier", lic.TierName(),
			"event", "components_licence_published")
	}
	return n, nil
}
