package broadcast

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Service runs publishing, unpublishing, the speed test and the token refresh.
type Service struct {
	Store       *Store
	Cloud       Cloud
	Publishable Publishable
	// HTTP makes the speed test's PUT to the edge (nil: a client with a 30 s limit).
	HTTP   *http.Client
	Logger *slog.Logger
	Now    func() time.Time
	// SpeedTestFor is how long the speed test sends (default 10 s).
	SpeedTestFor time.Duration
	// QuietHour is the local hour of the daily speed test (default 4).
	QuietHour int
	// Notice tells the home's admins something changed (the uplink rule dropped a rung,
	// a token expired); nil: logged only.
	Notice func(ctx context.Context, channelID, code string)
	// Revocations is the home's copy of Maktaba's broadcast revocation list (50.25); nil or
	// without keys: not honoured (the edge still refuses a revoked ingest).
	Revocations *Revocations
	// Moderation is Maktaba Cloud's takedowns and reports for this home (story 50.24); nil: the
	// home is not claimed, and no instruction is asked for.
	Moderation ModerationCloud
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) publishable() Publishable {
	if s.Publishable != nil {
		return s.Publishable
	}
	return MarkedPublishable{}
}

// Problem is an error the page shows with its code (broadcast.<code>) and values.
type Problem struct {
	Status  int
	Refusal Refusal
}

func (p *Problem) Error() string { return "broadcast: " + p.Refusal.Code }

func problem(status int, check, code string, params map[string]any) *Problem {
	return &Problem{Status: status, Refusal: Refusal{Check: check, Code: code, Params: params}}
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var countryRE = regexp.MustCompile(`^[A-Z]{2}$`)

// SetupRequest is PUT /api/tv/broadcast/channels/{id}.
type SetupRequest struct {
	Slug      string   `json:"slug"`
	Access    string   `json:"access"`
	Mode      string   `json:"mode"`
	Rungs     []string `json:"rungs"`
	Countries []string `json:"countries"`
	// GeoMode is how Countries read (story 50.23 §5): allow (only there, the default) or
	// deny (everywhere but there).
	GeoMode string `json:"geo_mode"`
}

// SaveSetup checks and stores a channel's setup. A published channel's setup is changed
// only by unpublishing it first (the cloud and the token carry its ladder).
func (s *Service) SaveSetup(ctx context.Context, id string, req SetupRequest) (Channel, error) {
	ch, err := s.Store.Get(ctx, id)
	if err != nil {
		return ch, err
	}
	if ch.Published() {
		return ch, problem(http.StatusConflict, "", "unpublishFirst", nil)
	}
	if req.Slug == "" {
		req.Slug = ch.Slug
	}
	if req.Access == "" {
		req.Access = "invite"
	}
	if req.Mode == "" {
		req.Mode = "on_demand"
	}
	if len(req.Rungs) == 0 {
		req.Rungs = append([]string(nil), DefaultLadder...)
	}
	if req.GeoMode == "" {
		req.GeoMode = GeoAllow
	}
	out := s.Store.Output(ctx)
	switch {
	case !slugRE.MatchString(req.Slug):
		return ch, problem(http.StatusBadRequest, "", "badSlug", nil)
	case req.Access != "invite" && req.Access != "open":
		return ch, problem(http.StatusBadRequest, "", "badAccess", nil)
	case req.Mode != "always_on" && req.Mode != "on_demand":
		return ch, problem(http.StatusBadRequest, "", "badMode", nil)
	case req.Access == "open" && req.Mode != "always_on":
		// open channels are always on (§2); on demand is for invite-only ones
		return ch, problem(http.StatusBadRequest, "", "openIsAlwaysOn", nil)
	case req.GeoMode != GeoAllow && req.GeoMode != GeoDeny:
		return ch, problem(http.StatusBadRequest, "", "badGeoMode", nil)
	}
	if err := ValidLadder(req.Rungs, out.Source); err != nil {
		return ch, problem(http.StatusBadRequest, "", "badLadder", map[string]any{"detail": err.Error()})
	}
	countries := make([]string, 0, len(req.Countries))
	for _, c := range req.Countries {
		c = strings.ToUpper(strings.TrimSpace(c))
		if !countryRE.MatchString(c) {
			return ch, problem(http.StatusBadRequest, "", "badCountry", map[string]any{"country": c})
		}
		countries = append(countries, c)
	}
	taken, err := s.Store.SlugTaken(ctx, id, req.Slug)
	if err != nil {
		return ch, err
	}
	if taken {
		return ch, problem(http.StatusConflict, "", "slugTaken", nil)
	}
	if err := s.Store.SaveSetup(ctx, id, req.Slug, Setup{Access: req.Access, Mode: req.Mode, Rungs: req.Rungs},
		Geo{Mode: req.GeoMode, Countries: countries}); err != nil {
		return ch, err
	}
	return s.Store.Get(ctx, id)
}

// Admin is who publishes: the API stores the name with the confirmation.
type Admin struct {
	UserID string
	Name   string
}

// Inputs gathers what the checks read for channel ch (nothing is changed).
func (s *Service) Inputs(ctx context.Context, ch Channel, plan Plan, confirmed bool) (Inputs, error) {
	out := s.Store.Output(ctx)
	set := s.Store.Settings(ctx)
	pub, err := s.Store.PublishedChannels(ctx)
	if err != nil {
		return Inputs{}, err
	}
	var others []Published
	for _, c := range pub {
		if c.ID == ch.ID {
			continue
		}
		at := time.Time{}
		if c.PublishedAt != nil {
			at = *c.PublishedAt
		}
		others = append(others, Published{ChannelID: c.ID, Rungs: c.Rungs, Source: out.Source, PublishedAt: at, Dropped: c.Dropped})
	}
	now := s.now()
	secs, err := s.publishable().Seconds(ctx, s.Store.DB, ch.ID, now, now.Add(24*time.Hour))
	if err != nil {
		return Inputs{}, err
	}
	up, err := s.Store.LatestUplink(ctx)
	if err != nil {
		return Inputs{}, err
	}
	inUse, sessions, capacity, err := s.Store.HostLoad(ctx, ch.ID, pub, set.RungSlotWeight)
	if err != nil {
		return Inputs{}, err
	}
	host := Host{MaxActive: out.MaxActive, InUse: inUse, Reserve: s.Store.ReserveSlots(ctx), SessionsInUse: sessions,
		Hardware: out.Encoder != "software"}
	if capacity != nil {
		host.MaxActive = *capacity
	}
	switch out.Encoder {
	case "nvenc":
		host.SessionCap = out.NVENCMax
	case "videotoolbox":
		host.SessionCap = out.VTMax
	}
	return Inputs{Plan: plan, Setup: Setup{Access: ch.Access, Mode: ch.Mode, Rungs: ch.Rungs}, Source: out.Source,
		Others: others, Settings: set, PublishableSeconds: secs, UplinkKbps: up.Kbps, Host: host,
		RightsConfirmed: confirmed}, nil
}

// cloudRefusal maps a refusal of the cloud's publish to the check it belongs to.
func cloudRefusal(err error) *Problem {
	var ce *CloudError
	if !errors.As(err, &ce) {
		if errors.Is(err, ErrUnreachable) {
			return problem(http.StatusServiceUnavailable, "", "cloudUnreachable", nil)
		}
		return nil
	}
	switch ce.Code {
	case "broadcast_needs_plan":
		return problem(http.StatusConflict, CheckPlan, CodeNeedsPlan, nil)
	case "broadcast_limit":
		if ce.Limit == "max_channels" {
			return problem(http.StatusConflict, CheckPlan, CodePlanChannels, map[string]any{"count": ce.Value})
		}
		return problem(http.StatusConflict, CheckPlan, "planLimit", map[string]any{"limit": ce.Limit, "value": ce.Value})
	case "broadcast_agreement_required":
		return problem(http.StatusConflict, CheckAgreement, CodeAgreement, nil)
	case "broadcast_not_enabled":
		return problem(http.StatusConflict, CheckPlan, "notOpen", nil)
	case "slug_taken":
		return problem(http.StatusConflict, "", "slugTaken", nil)
	}
	return problem(http.StatusBadGateway, "", "cloudRefused", map[string]any{"status": ce.Status, "error": ce.Code})
}

// Publish runs the six checks in order and, when they pass, stores the rights
// confirmation, publishes on the cloud and fetches the ingest token. The front door's
// broadcaster starts on its own once the channel is live. A refusal starts nothing.
func (s *Service) Publish(ctx context.Context, id string, confirmed bool, by Admin) (Channel, error) {
	ch, err := s.Store.Get(ctx, id)
	if err != nil {
		return ch, err
	}
	if !ch.Configured {
		return ch, problem(http.StatusConflict, "", "setupFirst", nil)
	}
	if ch.Published() {
		return ch, nil
	}
	if p := s.revocationRefusal(ctx, ch); p != nil {
		return ch, p
	}
	plan, err := s.Cloud.State(ctx)
	if err != nil {
		if p := cloudRefusal(err); p != nil {
			return ch, p
		}
		return ch, err
	}
	in, err := s.Inputs(ctx, ch, plan, confirmed)
	if err != nil {
		return ch, err
	}
	if r := Run(in); r != nil {
		return ch, &Problem{Status: http.StatusConflict, Refusal: *r}
	}
	at, err := s.Store.AddConfirmation(ctx, id, plan.AgreementVersion, by.Name, by.UserID)
	if err != nil {
		return ch, err
	}
	cc, err := s.Cloud.Publish(ctx, PublishRequest{Slug: ch.Slug, Access: ch.Access, OnDemand: ch.Mode == "on_demand",
		Rungs: len(ch.Rungs), TopKbps: TopKbps(ch.Rungs, in.Source), RightsConfirmedAt: at.UTC(),
		AgreementVersion: plan.AgreementVersion})
	if err != nil {
		if p := cloudRefusal(err); p != nil {
			return ch, p
		}
		return ch, err
	}
	state, detail := StateLive, ""
	if err := s.refreshToken(ctx, ch); err != nil {
		// published on the cloud; the refresher keeps asking, the edge plays the slate
		s.log().Warn("broadcast: no ingest token yet", "channel_id", id, "err", err, "event", "broadcast_token_failed")
		state, detail = StateOffline, "edgeUnavailable"
	}
	if err := s.Store.MarkPublished(ctx, id, cc.ID, state, detail, by.Name); err != nil {
		return ch, err
	}
	// what was planned from the full rules is rewritten from publishable items (§3 of 50.23)
	s.RescheduleLimited(ctx, id)
	s.log().Info("broadcast: channel published", "channel_id", id, "slug", ch.Slug, "access", ch.Access,
		"mode", ch.Mode, "rungs", strings.Join(ch.Rungs, ","), "event", "broadcast_published")
	return s.Store.Get(ctx, id)
}

// Unpublish stops the channel: the token goes, the front door's broadcaster stops at its
// next sync, and the cloud marks it unpublished (later, when it cannot be reached now).
func (s *Service) Unpublish(ctx context.Context, id string) (Channel, error) {
	ch, err := s.Store.Get(ctx, id)
	if err != nil {
		return ch, err
	}
	if !ch.Configured || ch.State == StateDraft {
		return ch, nil
	}
	pending := false
	if ch.CloudID != "" {
		if err := s.Cloud.Unpublish(ctx, ch.CloudID); err != nil {
			pending = true
			s.log().Warn("broadcast: the cloud did not take the unpublish; retried", "channel_id", id, "err", err,
				"event", "broadcast_unpublish_pending")
		}
	}
	if err := s.Store.MarkUnpublished(ctx, id, pending); err != nil {
		return ch, err
	}
	// the full rules come back from the block on air (50.23 §3)
	s.RescheduleLimited(ctx, id)
	s.log().Info("broadcast: channel unpublished", "channel_id", id, "event", "broadcast_unpublished")
	return s.Store.Get(ctx, id)
}

func (s *Service) refreshToken(ctx context.Context, ch Channel) error {
	tok, err := s.Cloud.IngestToken(ctx, ch.ID, Effective(ch.Rungs, ch.Dropped), ch.Geo())
	if err != nil {
		return err
	}
	if tok.Token == "" || tok.IngestURL == "" {
		return errors.New("broadcast: the cloud sent an empty token")
	}
	return s.Store.SaveToken(ctx, ch.ID, tok)
}

// RefreshEvery is how often a published channel's token is asked for again (§2).
const RefreshEvery = time.Hour

// Tick refreshes due tokens, retries pending unpublishes, honours the revocation list, applies
// Maktaba's takedown instructions and runs the daily speed test.
func (s *Service) Tick(ctx context.Context) {
	s.applyRevocations(ctx)
	s.applyTakedowns(ctx)
	pending, err := s.Store.PendingUnpublish(ctx)
	if err == nil {
		for id, cloud := range pending {
			if s.Cloud.Unpublish(ctx, cloud) == nil {
				_ = s.Store.ClearCloudID(ctx, id)
			}
		}
	}
	pub, err := s.Store.PublishedChannels(ctx)
	if err != nil {
		s.log().Warn("broadcast: reading the published channels failed", "err", err, "event", "broadcast_tick_failed")
		return
	}
	now := s.now()
	for _, ch := range pub {
		fetched, expires, err := s.Store.TokenAge(ctx, ch.ID)
		if err != nil {
			continue
		}
		if !fetched.IsZero() && now.Sub(fetched) < RefreshEvery && ch.Detail == "" {
			continue
		}
		if err := s.refreshToken(ctx, ch); err != nil {
			// the cloud is away: the current token carries on until it expires (24 h)
			if !expires.IsZero() && !now.Before(expires) && ch.Detail != "tokenExpired" {
				_ = s.Store.SetState(ctx, ch.ID, StateOffline, "tokenExpired")
				s.notice(ctx, ch.ID, "tokenExpired")
			}
			continue
		}
		if ch.State == StateOffline && (ch.Detail == "tokenExpired" || ch.Detail == "edgeUnavailable") {
			_ = s.Store.SetState(ctx, ch.ID, StateLive, "")
		}
	}
	if len(pub) > 0 && now.Hour() == s.quietHour() {
		up, err := s.Store.LatestUplink(ctx)
		if err == nil && (up.MeasuredAt == nil || now.Sub(*up.MeasuredAt) > 20*time.Hour) {
			if _, err := s.SpeedTest(ctx, "daily"); err != nil {
				s.log().Warn("broadcast: the daily speed test failed", "err", err, "event", "broadcast_speedtest_failed")
			}
		}
	}
}

func (s *Service) quietHour() int {
	if s.QuietHour > 0 {
		return s.QuietHour
	}
	return 4
}

func (s *Service) notice(ctx context.Context, channelID, code string) {
	s.log().Warn("broadcast: "+code, "channel_id", channelID, "code", code, "event", "broadcast_notice")
	if s.Notice != nil {
		s.Notice(ctx, channelID, code)
	}
}

// Run ticks every minute until ctx ends (the api:broadcast_tokens loop).
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SpeedTest measures the upload to the edge (PUT /v1/ingest/speedtest for 10 s), stores it,
// and applies the uplink rule to the published channels (§3).
func (s *Service) SpeedTest(ctx context.Context, source string) (Uplink, error) {
	token, ingest, err := s.Store.AnyToken(ctx)
	if err != nil {
		return Uplink{}, err
	}
	if token == "" {
		// no channel published yet: a token for the first configured channel
		chs, err := s.Store.List(ctx)
		if err != nil {
			return Uplink{}, err
		}
		for _, c := range chs {
			if c.Configured {
				tok, terr := s.Cloud.IngestToken(ctx, c.ID, c.Rungs, c.Geo())
				if terr != nil {
					if p := cloudRefusal(terr); p != nil {
						return Uplink{}, p
					}
					return Uplink{}, terr
				}
				token, ingest = tok.Token, tok.IngestURL
				break
			}
		}
	}
	if token == "" {
		return Uplink{}, problem(http.StatusConflict, "", "setupFirst", nil)
	}
	kbps, err := s.measure(ctx, ingest, token)
	if err != nil {
		return Uplink{}, problem(http.StatusBadGateway, "", "speedTestFailed", map[string]any{"detail": err.Error()})
	}
	if err := s.Store.AddUplink(ctx, kbps, source); err != nil {
		return Uplink{}, err
	}
	if pub, err := s.Store.PublishedChannels(ctx); err == nil {
		out := s.Store.Output(ctx)
		var ps []Published
		for _, c := range pub {
			at := time.Time{}
			if c.PublishedAt != nil {
				at = *c.PublishedAt
			}
			ps = append(ps, Published{ChannelID: c.ID, Rungs: c.Rungs, Source: out.Source, PublishedAt: at, Dropped: c.Dropped})
		}
		for _, d := range DropsFor(ps, kbps, s.Store.Settings(ctx).UplinkShare) {
			if err := s.Store.SetDropped(ctx, d.ChannelID, d.Dropped); err == nil {
				s.notice(ctx, d.ChannelID, "rungDropped")
			}
		}
	}
	return s.Store.LatestUplink(ctx)
}

// measure sends random bytes for SpeedTestFor and reads the edge's {kbps}.
func (s *Service) measure(ctx context.Context, ingestURL, token string) (int, error) {
	dur := s.SpeedTestFor
	if dur <= 0 {
		dur = 10 * time.Second
	}
	pr, pw := io.Pipe()
	go func() {
		buf := make([]byte, 64<<10)
		_, _ = rand.Read(buf)
		deadline := time.Now().Add(dur)
		for time.Now().Before(deadline) {
			if _, err := pw.Write(buf); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()
	rctx, cancel := context.WithTimeout(ctx, dur+20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPut, strings.TrimRight(ingestURL, "/")+"/v1/ingest/speedtest", pr)
	if err != nil {
		_ = pr.Close()
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Maktaba-Time", strconv.FormatInt(s.now().UnixMilli(), 10))
	req.Header.Set("Content-Type", "application/octet-stream")
	cl := s.HTTP
	if cl == nil {
		cl = &http.Client{Timeout: dur + 30*time.Second}
	}
	resp, err := cl.Do(req)
	_ = pr.Close()
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Kbps int `json:"kbps"`
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("the edge answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil || out.Kbps <= 0 {
		return 0, fmt.Errorf("the edge's answer had no rate")
	}
	return out.Kbps, nil
}
