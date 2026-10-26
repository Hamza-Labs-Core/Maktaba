package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// RevocationsEvery is how often the revocation list is fetched (story
// 16.4: daily).
const RevocationsEvery = 24 * time.Hour

// CloudCheckEvery is how often a home claimed in Maktaba Cloud asks for
// its plan's license. Story 16.4 asks for daily; story 16.3 wants an
// upgrade to unlock within 60 s of the payment, so the home asks every
// minute (one small request through its own relay agent).
const CloudCheckEvery = time.Minute

// Refresher keeps the applied license in step with the license server
// (Maktaba Cloud): the signed revocation list daily, and, for a home
// claimed in the cloud, the license its owner's plan grants.
type Refresher struct {
	Store    *Store
	Verifier *Verifier
	// ServerURL is the license server's base URL ("" ⇒ none): the
	// cloud this home is claimed in, else the configured default.
	ServerURL func() string
	// AgentAddr is the relay agent's loopback admin address ("" ⇒ the
	// home is not claimed in the cloud; no plan license to fetch).
	AgentAddr string
	// ServerID is this home's id in the cloud ("" ⇒ not claimed).
	ServerID func() string
	Client   *http.Client
	Log      *slog.Logger
	// Event, when set, records a change the refresh made (audit).
	Event func(ctx context.Context, event string, detail map[string]any)

	kick chan struct{}
}

// Kick asks for a refresh soon. Never blocks.
func (r *Refresher) Kick() {
	if r == nil || r.kick == nil {
		return
	}
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run refreshes at start, after each Kick and every CloudCheckEvery
// until ctx ends.
func (r *Refresher) Run(ctx context.Context) {
	if r.kick == nil {
		r.kick = make(chan struct{}, 1)
	}
	t := time.NewTicker(CloudCheckEvery)
	defer t.Stop()
	force := true
	for {
		if err := r.refresh(ctx, force); err != nil && r.Log != nil {
			r.Log.Warn("license: refresh with the license server failed; the last answer stands",
				"err", err, "event", "license_refresh_failed")
		}
		force = false
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
			force = true
		case <-t.C:
		}
	}
}

// RefreshNow fetches the revocation list and the plan license now
// (Settings → "Check now").
func (r *Refresher) RefreshNow(ctx context.Context) error {
	return r.refresh(ctx, true)
}

func (r *Refresher) refresh(ctx context.Context, force bool) error {
	if r == nil || r.Store == nil || r.Verifier == nil {
		return nil
	}
	var errs []string
	contacted := false
	st := r.Store.State()
	if base := r.serverURL(); base != "" && (force || st.RevocationsAt.IsZero() || r.Store.Now().Sub(st.RevocationsAt) >= RevocationsEvery) {
		if err := r.fetchRevocations(ctx, base); err != nil {
			errs = append(errs, err.Error())
		} else {
			contacted = true
		}
	}
	if r.AgentAddr != "" && r.serverID() != "" {
		if err := r.fetchPlanLicense(ctx); err != nil {
			errs = append(errs, err.Error())
		} else {
			contacted = true
		}
	}
	if !contacted && len(errs) == 0 {
		return nil // nothing to ask (no license server configured)
	}
	var err error
	if len(errs) > 0 {
		err = errors.New(strings.Join(errs, "; "))
	}
	if serr := r.Store.RecordRefresh(ctx, err); serr != nil && err == nil {
		err = serr
	}
	return err
}

func (r *Refresher) serverURL() string {
	if r.ServerURL == nil {
		return ""
	}
	return strings.TrimRight(r.ServerURL(), "/")
}

func (r *Refresher) serverID() string {
	if r.ServerID == nil {
		return ""
	}
	return r.ServerID()
}

func (r *Refresher) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (r *Refresher) get(ctx context.Context, url string) (int, []byte, time.Time, error) {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, time.Time{}, err
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return 0, nil, time.Time{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, time.Time{}, err
	}
	date, _ := http.ParseTime(resp.Header.Get("Date"))
	return resp.StatusCode, body, date, nil
}

func (r *Refresher) fetchRevocations(ctx context.Context, base string) error {
	code, body, date, err := r.get(ctx, base+"/v1/licenses/revocations")
	if err != nil {
		return fmt.Errorf("the license server is unreachable: %w", err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("the license server answered %d for the revocation list", code)
	}
	var list RevocationList
	if err := json.Unmarshal(body, &list); err != nil {
		return errors.New("the license server sent an unreadable revocation list")
	}
	if err := r.Verifier.VerifyRevocations(&list); err != nil {
		return errors.New("the revocation list's signature does not match the license key")
	}
	r.Store.ObserveServerTime(date)
	before := r.Store.State().Status
	if err := r.Store.SetRevocations(ctx, &list, string(body), r.Store.Now()); err != nil {
		return err
	}
	if after := r.Store.State(); before != StatusRevoked && after.Status == StatusRevoked {
		r.event(ctx, "license.revoked", map[string]any{"license_id": after.License.LicenseID, "source": "revocation_list"})
		if r.Log != nil {
			r.Log.Warn("license: the applied license is on the license server's revocation list; this home is on the free tier",
				"license_id", after.License.LicenseID, "event", "license_revoked")
		}
	}
	return nil
}

// planLicenseResp is the agent's pass-through of the cloud's answer to
// GET /v1/servers/{id}/license.
type planLicenseResp struct {
	License *License `json:"license"`
	Error   string   `json:"error"`
}

var tierRank = map[Tier]int{TierFree: 0, TierHome: 1, TierPro: 2}

func (r *Refresher) fetchPlanLicense(ctx context.Context) error {
	code, body, date, err := r.get(ctx, "http://"+r.AgentAddr+"/admin/cloud-link/license")
	if err != nil {
		return fmt.Errorf("the relay agent is unreachable: %w", err)
	}
	var resp planLicenseResp
	_ = json.Unmarshal(body, &resp)
	st := r.Store.State()
	cloudBound := st.License != nil && st.License.ServerID != ""
	switch {
	case code == http.StatusOK && resp.License != nil:
		r.Store.ObserveServerTime(date)
		return r.applyPlanLicense(ctx, resp.License, st)
	case code == http.StatusNotFound && resp.Error == "no_license":
		// the owner's plan grants no license: a plan license ends; an
		// offline license someone applied by hand is not the cloud's
		if cloudBound {
			if err := r.Store.Revoke(ctx); err != nil {
				return err
			}
			r.event(ctx, "license.plan_ended", map[string]any{"license_id": st.License.LicenseID})
		}
		return nil
	case code == http.StatusForbidden && resp.Error == "license_revoked":
		if cloudBound && st.LockReason != LockRevoked {
			if err := r.Store.Lock(ctx, LockRevoked); err != nil {
				return err
			}
			r.event(ctx, "license.revoked", map[string]any{"license_id": st.License.LicenseID, "source": "cloud"})
		}
		return nil
	default:
		return fmt.Errorf("the cloud answered %d for this home's license", code)
	}
}

func (r *Refresher) applyPlanLicense(ctx context.Context, lic *License, st State) error {
	ent, err := r.Verifier.Verify(lic, r.Store.Now())
	if err != nil {
		return fmt.Errorf("the cloud sent a license this home cannot verify: %w", err)
	}
	if ent.ServerID != r.serverID() {
		return errors.New("the cloud sent a license for another home")
	}
	// the same license again changes nothing, unless the cloud had
	// revoked it (a dispute since resolved): applying clears that lock
	if lic.Signature == r.Store.Fingerprint() && st.LockReason != LockRevoked {
		return nil
	}
	// a plan license does not displace a working offline license of a
	// higher tier someone applied by hand
	if st.Effective != nil && st.Effective.ServerID == "" && tierRank[st.Effective.Tier] > tierRank[ent.Tier] {
		return nil
	}
	if err := r.Store.SetLicense(ctx, lic, ent); err != nil {
		return err
	}
	r.event(ctx, "license.applied", map[string]any{"license_id": ent.LicenseID, "tier": string(ent.Tier), "source": "cloud"})
	return nil
}

func (r *Refresher) event(ctx context.Context, ev string, detail map[string]any) {
	if r.Event != nil {
		r.Event(ctx, ev, detail)
	}
}
