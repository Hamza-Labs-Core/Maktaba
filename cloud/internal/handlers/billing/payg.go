package billing

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// Story 50.19 §5, §6: the account owner's pay-as-you-go and extra streams (Pro only).
//
//	GET /v1/billing/payg            {enabled, cap_cents, spent_cents, price_per_gb_cents}
//	PUT /v1/billing/payg            {enabled, cap_cents}: on needs a cap; item 2 at the provider
//	PUT /v1/billing/extra-streams   {count}: item 3's quantity at the provider (prorated); the
//	                                webhook then sets the relay's limit
func mountPAYG(r interface {
	Get(string, http.HandlerFunc)
	Put(string, http.HandlerFunc)
}, d *Deps) {
	r.Get("/v1/billing/payg", d.GetPAYG)
	r.Put("/v1/billing/payg", d.PutPAYG)
	r.Put("/v1/billing/extra-streams", d.PutExtraStreams)
}

type paygView struct {
	Enabled    bool  `json:"enabled"`
	CapCents   int64 `json:"cap_cents"`
	SpentCents int64 `json:"spent_cents"`
	// PricePerGB is a GB's price in cents; null: not set yet, pay-as-you-go can't be on
	PricePerGB *int `json:"price_per_gb_cents"`
}

// GetPAYG is the caller's pay-as-you-go: the settings and what this month's cost so far.
func (d *Deps) GetPAYG(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	store := &billingpkg.PAYGStore{DB: d.DB}
	p, err := store.Get(r.Context(), uid)
	if err != nil {
		d.fail(w, r, "load pay as you go", err)
		return
	}
	bytes, err := store.SpentBytes(r.Context(), uid, d.now())
	if err != nil {
		d.fail(w, r, "load pay as you go", err)
		return
	}
	price := billingpkg.Tiers[billingpkg.PlanPro].PAYGPricePerGBCents
	v := paygView{Enabled: p.Enabled, CapCents: p.CapCents, PricePerGB: price}
	if price != nil {
		v.SpentCents = billingpkg.PAYGSpentCents(bytes, int64(*price))
	}
	writeJSON(w, http.StatusOK, v)
}

// proSubscription is the caller's live Pro subscription, or an answer saying why there is none.
func (d *Deps) proSubscription(w http.ResponseWriter, r *http.Request) (subID string, ok bool) {
	p := d.provider()
	if !p.Enabled() {
		paymentsNotSetUp(w)
		return "", false
	}
	s, found, err := d.subscription(r, middleware.GetUserID(r.Context()))
	if err != nil {
		d.fail(w, r, "load subscription", err)
		return "", false
	}
	if !found || s.Plan != billingpkg.PlanPro || !liveStatuses[s.Status] || !s.SubscriptionID.Valid {
		writeErr(w, http.StatusConflict, "needs_pro", "pay as you go and extra streams come with the Pro plan")
		return "", false
	}
	return s.SubscriptionID.String, true
}

// PutPAYG turns pay-as-you-go on or off and sets its cap. On adds item 2 at the provider (and
// needs a cap); off removes it. A cap lowered below what is spent stops it at once.
func (d *Deps) PutPAYG(w http.ResponseWriter, r *http.Request) {
	var body billingpkg.PAYGSettings
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {enabled, cap_cents}")
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
	if body.Enabled && billingpkg.Tiers[billingpkg.PlanPro].PAYGPricePerGBCents == nil {
		writeErr(w, http.StatusConflict, "price_not_set", "pay as you go has no price yet")
		return
	}
	sub, ok := d.proSubscription(w, r)
	if !ok {
		return
	}
	uid := middleware.GetUserID(r.Context())
	extra := 0
	_ = d.DB.QueryRowContext(r.Context(), `SELECT extra_streams FROM subscriptions WHERE user_id = $1`, uid).Scan(&extra)
	if err := d.provider().SetItems(r.Context(), sub, body.Enabled, extra); err != nil {
		if errors.Is(err, billingpkg.ErrNotConfigured) {
			paymentsNotSetUp(w)
			return
		}
		writeErr(w, http.StatusBadGateway, "provider_failed", "the payment provider is unavailable; try again")
		return
	}
	if err := (&billingpkg.PAYGStore{DB: d.DB}).Set(r.Context(), uid, body); err != nil {
		d.fail(w, r, "save pay as you go", err)
		return
	}
	d.GetPAYG(w, r)
}

// PutExtraStreams sets how many relayed streams beyond the plan's the owner buys (item 3). The
// provider prorates; its webhook sets the relay's limit.
func (d *Deps) PutExtraStreams(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil || body.Count < 0 || body.Count > 100 {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {count}, 0 to 100")
		return
	}
	if billingpkg.Tiers[billingpkg.PlanPro].ExtraStreamPriceCents == nil {
		writeErr(w, http.StatusConflict, "price_not_set", "extra streams have no price yet")
		return
	}
	sub, ok := d.proSubscription(w, r)
	if !ok {
		return
	}
	p, err := (&billingpkg.PAYGStore{DB: d.DB}).Get(r.Context(), middleware.GetUserID(r.Context()))
	if err != nil {
		d.fail(w, r, "load pay as you go", err)
		return
	}
	if err := d.provider().SetItems(r.Context(), sub, p.Enabled, body.Count); err != nil {
		if errors.Is(err, billingpkg.ErrNotConfigured) {
			paymentsNotSetUp(w)
			return
		}
		writeErr(w, http.StatusBadGateway, "provider_failed", "the payment provider is unavailable; try again")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"count": body.Count})
}
