package billing

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// Story 50.20 §4, §5: a home's broadcasting on its owner's Billing page. Plans are per home, so
// every call names the home, and only the account that owns it may read or change it (403
// not_owner for anyone else, a user of the home included).
//
//	GET   /v1/billing/broadcast?server_id=     the tiers, the home's plan, its usage and mode
//	PUT   /v1/billing/broadcast                {server_id, enabled}: the Broadcaster add-on (item 4)
//	PATCH /v1/billing/broadcast/payg           {server_id, enabled, cap_cents}: item 5
//	PATCH /v1/billing/broadcast/extras         {server_id, extra_channels, extra_viewers_100}: items 6, 7
//
// The provider prorates and its webhook confirms; the home's limits follow from the webhook.
func mountBroadcast(r interface {
	Get(string, http.HandlerFunc)
	Put(string, http.HandlerFunc)
	Patch(string, http.HandlerFunc)
}, d *Deps) {
	r.Get("/v1/billing/broadcast", d.GetBroadcast)
	r.Put("/v1/billing/broadcast", d.PutBroadcaster)
	r.Patch("/v1/billing/broadcast/payg", d.PutBroadcastPAYG)
	r.Patch("/v1/billing/broadcast/extras", d.PutBroadcastExtras)
}

type broadcastTierView struct {
	ID                   string   `json:"id"`
	MaxChannels          int      `json:"max_channels"`
	Access               []string `json:"access"`
	OnDemandOnly         bool     `json:"on_demand_only"`
	MaxConcurrentViewers int      `json:"max_concurrent_viewers"`
	ViewerHoursPerMonth  int      `json:"viewer_hours_per_month"`
	MaxRungs             int      `json:"max_rungs"`
	MaxTopKbps           int      `json:"max_top_kbps"`
	PAYG                 bool     `json:"pay_as_you_go"`
	FairUseMultiplier    float64  `json:"fair_use_multiplier"`
	// prices in cents; null: not set yet ("price coming soon")
	PriceCents            *int `json:"price_cents"`
	PAYGPricePer100HCents *int `json:"payg_price_per_100h_cents"`
	ExtraChannelCents     *int `json:"extra_channel_price_cents"`
	ExtraViewers100Cents  *int `json:"extra_viewers_100_price_cents"`
}

func tierView(t billingpkg.BroadcastTier) broadcastTierView {
	return broadcastTierView{ID: t.ID, MaxChannels: t.MaxChannels, Access: t.Access, OnDemandOnly: t.OnDemandOnly,
		MaxConcurrentViewers: t.MaxConcurrentViewers, ViewerHoursPerMonth: t.ViewerHoursPerMonth,
		MaxRungs: t.MaxRungs, MaxTopKbps: t.MaxTopKbps, PAYG: t.PAYG, FairUseMultiplier: t.FairUseMultiplier,
		PriceCents: t.PriceCents, PAYGPricePer100HCents: t.PAYGPricePer100HCents,
		ExtraChannelCents: t.ExtraChannelPriceCents, ExtraViewers100Cents: t.ExtraViewers100Cents}
}

// BroadcastTiers is the broadcast catalog for the plan comparison: guest sharing (in Pro) and
// the Broadcaster add-on.
func BroadcastTiers() map[string]any {
	return map[string]any{"guest": tierView(billingpkg.GuestSharing), "broadcaster": tierView(billingpkg.Broadcaster)}
}

type broadcastView struct {
	ServerID string                           `json:"server_id"`
	Tiers    map[string]any                   `json:"tiers"`
	State    billingpkg.PublisherState        `json:"state"`
	PAYG     billingpkg.BroadcastPAYGSettings `json:"payg"`
	Extras   struct {
		Channels   int `json:"channels"`
		Viewers100 int `json:"viewers_100"`
	} `json:"extras"`
	Agreement struct {
		Version  int  `json:"version"`
		Accepted bool `json:"accepted"`
	} `json:"agreement"`
	// Enabled is the broadcasting gate for this home (broadcast.enabled, or a test publisher)
	Enabled bool `json:"enabled"`
	// Purchasable: the add-on can be bought now (a provider, every price set)
	Purchasable bool `json:"purchasable"`
}

// ownedPublisher is the home named by server_id when the caller owns it; false after answering
// 403 not_owner (a home that doesn't exist answers the same, so nothing is learned).
func (d *Deps) ownedPublisher(w http.ResponseWriter, r *http.Request, serverID string) (billingpkg.Publisher, bool) {
	if d.Broadcast == nil {
		writeErr(w, http.StatusServiceUnavailable, "broadcast_unavailable", "")
		return billingpkg.Publisher{}, false
	}
	p, err := d.Broadcast.Publisher(r.Context(), serverID)
	if err != nil && !errors.Is(err, billingpkg.ErrUnknownPublisher) {
		d.fail(w, r, "load home", err)
		return billingpkg.Publisher{}, false
	}
	if err != nil || p.OwnerID != middleware.GetUserID(r.Context()) {
		writeErr(w, http.StatusForbidden, "not_owner", "only the account that owns this home can change its broadcasting")
		return billingpkg.Publisher{}, false
	}
	return p, true
}

// GetBroadcast is a home's broadcasting on the Billing page.
func (d *Deps) GetBroadcast(w http.ResponseWriter, r *http.Request) {
	p, ok := d.ownedPublisher(w, r, r.URL.Query().Get("server_id"))
	if !ok {
		return
	}
	ctx := r.Context()
	st, err := d.Broadcast.State(ctx, p.ServerID)
	if err != nil {
		d.fail(w, r, "broadcast state", err)
		return
	}
	set, err := d.Broadcast.PAYGSettings(ctx, p.ServerID)
	if err != nil {
		d.fail(w, r, "broadcast payg", err)
		return
	}
	accepted, err := d.Broadcast.AgreementAccepted(ctx, p.OwnerID)
	if err != nil {
		d.fail(w, r, "agreement", err)
		return
	}
	v := broadcastView{ServerID: p.ServerID, Tiers: BroadcastTiers(), State: st, PAYG: set, Enabled: d.Broadcast.Gated(p)}
	v.Extras.Channels, v.Extras.Viewers100 = p.ExtraChannels, p.ExtraViewers100
	v.Agreement.Version, v.Agreement.Accepted = billingpkg.BroadcastAgreementVersion, accepted
	v.Purchasable = d.provider().Enabled() && billingpkg.BroadcastLaunchCheck(billingpkg.Tiers, billingpkg.Broadcaster) == nil
	writeJSON(w, http.StatusOK, v)
}

// setBroadcastItems sends a home's items to the provider, answering on failure.
func (d *Deps) setBroadcastItems(w http.ResponseWriter, r *http.Request, sub string, it billingpkg.BroadcastItems) bool {
	if err := d.provider().SetBroadcastItems(r.Context(), sub, it); err != nil {
		if errors.Is(err, billingpkg.ErrNotConfigured) {
			paymentsNotSetUp(w)
			return false
		}
		writeErr(w, http.StatusBadGateway, "provider_failed", "the payment provider is unavailable; try again")
		return false
	}
	return true
}

// PutBroadcaster adds or removes the Broadcaster add-on on a Pro home. Buying it needs the
// Broadcaster agreement accepted first (409 broadcast_agreement_required) and every broadcast
// price set (409 price_not_set). Removing it removes its pay-as-you-go and extras too.
func (d *Deps) PutBroadcaster(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerID string `json:"server_id"`
		Enabled  bool   `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {server_id, enabled}")
		return
	}
	p, ok := d.ownedPublisher(w, r, body.ServerID)
	if !ok {
		return
	}
	if body.Enabled {
		if err := billingpkg.BroadcastLaunchCheck(billingpkg.Tiers, billingpkg.Broadcaster); err != nil {
			writeErr(w, http.StatusConflict, "price_not_set", "Broadcaster has no price yet")
			return
		}
		accepted, err := d.Broadcast.AgreementAccepted(r.Context(), p.OwnerID)
		if err != nil {
			d.fail(w, r, "agreement", err)
			return
		}
		if !accepted {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "broadcast_agreement_required",
				"message": "accept the Broadcaster agreement first", "version": billingpkg.BroadcastAgreementVersion})
			return
		}
	}
	sub, ok := d.proSubscription(w, r)
	if !ok {
		return
	}
	it := billingpkg.BroadcastItems{ServerID: p.ServerID, Broadcaster: body.Enabled}
	if body.Enabled {
		set, err := d.Broadcast.PAYGSettings(r.Context(), p.ServerID)
		if err != nil {
			d.fail(w, r, "broadcast payg", err)
			return
		}
		it.PAYG, it.ExtraChannels, it.ExtraViewers100 = set.Enabled, p.ExtraChannels, p.ExtraViewers100
	}
	if !d.setBroadcastItems(w, r, sub, it) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"server_id": p.ServerID, "broadcaster": body.Enabled})
}

// broadcasterHome is the home when it has the add-on; false after 409 needs_broadcaster.
func (d *Deps) broadcasterHome(w http.ResponseWriter, r *http.Request, serverID string) (billingpkg.Publisher, bool) {
	p, ok := d.ownedPublisher(w, r, serverID)
	if !ok {
		return p, false
	}
	if t := p.Tier(); t == nil || t.ID != billingpkg.BroadcastPlanBroadcaster {
		writeErr(w, http.StatusConflict, "needs_broadcaster", "pay as you go, extra channels and extra viewers come with Broadcaster")
		return p, false
	}
	return p, true
}

// PutBroadcastPAYG turns a Broadcaster home's pay-as-you-go viewer-hours on or off and sets its
// monthly cap. On needs a cap; a cap lowered below what is spent stops it at once.
func (d *Deps) PutBroadcastPAYG(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerID string `json:"server_id"`
		billingpkg.BroadcastPAYGSettings
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {server_id, enabled, cap_cents}")
		return
	}
	if body.Enabled && body.CapCents <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "cap_required", billingpkg.ErrPAYGCapRequired.Error())
		return
	}
	if body.CapCents < 0 || body.CapCents > 1_000_000_00 {
		writeErr(w, http.StatusUnprocessableEntity, "bad_cap", "the monthly limit is out of range")
		return
	}
	p, ok := d.broadcasterHome(w, r, body.ServerID)
	if !ok {
		return
	}
	if body.Enabled && billingpkg.BroadcastPAYGPrice() <= 0 {
		writeErr(w, http.StatusConflict, "price_not_set", "pay as you go has no price yet")
		return
	}
	sub, ok := d.proSubscription(w, r)
	if !ok {
		return
	}
	if !d.setBroadcastItems(w, r, sub, billingpkg.BroadcastItems{ServerID: p.ServerID, Broadcaster: true, PAYG: body.Enabled,
		ExtraChannels: p.ExtraChannels, ExtraViewers100: p.ExtraViewers100}) {
		return
	}
	if err := d.Broadcast.SetPAYGSettings(r.Context(), p.ServerID, body.BroadcastPAYGSettings); err != nil {
		d.fail(w, r, "save broadcast payg", err)
		return
	}
	writeJSON(w, http.StatusOK, body.BroadcastPAYGSettings)
}

// PutBroadcastExtras sets how many channels and 100-viewer blocks beyond Broadcaster's the
// owner buys (items 6, 7). The provider prorates; its webhook sets the home's limits.
func (d *Deps) PutBroadcastExtras(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerID        string `json:"server_id"`
		ExtraChannels   int    `json:"extra_channels"`
		ExtraViewers100 int    `json:"extra_viewers_100"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil ||
		body.ExtraChannels < 0 || body.ExtraChannels > 100 || body.ExtraViewers100 < 0 || body.ExtraViewers100 > 100 {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {server_id, extra_channels, extra_viewers_100}, each 0 to 100")
		return
	}
	p, ok := d.broadcasterHome(w, r, body.ServerID)
	if !ok {
		return
	}
	b := billingpkg.Broadcaster
	if (body.ExtraChannels > 0 && b.ExtraChannelPriceCents == nil) || (body.ExtraViewers100 > 0 && b.ExtraViewers100Cents == nil) {
		writeErr(w, http.StatusConflict, "price_not_set", "extra channels and viewers have no price yet")
		return
	}
	sub, ok := d.proSubscription(w, r)
	if !ok {
		return
	}
	set, err := d.Broadcast.PAYGSettings(r.Context(), p.ServerID)
	if err != nil {
		d.fail(w, r, "broadcast payg", err)
		return
	}
	if !d.setBroadcastItems(w, r, sub, billingpkg.BroadcastItems{ServerID: p.ServerID, Broadcaster: true, PAYG: set.Enabled,
		ExtraChannels: body.ExtraChannels, ExtraViewers100: body.ExtraViewers100}) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"server_id": p.ServerID, "extra_channels": body.ExtraChannels,
		"extra_viewers_100": body.ExtraViewers100})
}
