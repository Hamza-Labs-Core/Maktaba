package broadcast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Cloud is what the service asks of Maktaba Cloud (the relay agent in production, a fake
// in tests).
type Cloud interface {
	State(ctx context.Context) (Plan, error)
	Publish(ctx context.Context, req PublishRequest) (CloudChannel, error)
	Unpublish(ctx context.Context, cloudID string) error
	// IngestToken asks for a published channel's token, named by its cloud id (required: the
	// cloud binds the channel's edge id to it, story 50.24).
	IngestToken(ctx context.Context, channel, cloudID string, rungs []string, geo *Geo) (IngestToken, error)
	// SpeedTestToken asks for a speed-test token: the upload measured before the first publish,
	// good on the edge's speed test only.
	SpeedTestToken(ctx context.Context, channel string) (IngestToken, error)
}

// ErrNoCloudID is a token asked for a channel the cloud has not given an id (not published).
var ErrNoCloudID = errors.New("broadcast: the channel has no cloud id; publish it first")

// PublishRequest is POST /v1/broadcast/channels (story 50.20's handler).
type PublishRequest struct {
	Slug     string `json:"slug"`
	Access   string `json:"access"`
	OnDemand bool   `json:"on_demand"`
	Rungs    int    `json:"rungs"`
	TopKbps  int    `json:"top_kbps"`
	// RightsConfirmedAt and AgreementVersion are all the cloud learns of the confirmation.
	RightsConfirmedAt time.Time `json:"rights_confirmed_at"`
	AgreementVersion  int       `json:"agreement_version"`
}

// CloudChannel is the cloud's answer to a publish.
type CloudChannel struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

// IngestToken is POST /v1/broadcast/ingest-token's answer (story 50.21 §2).
type IngestToken struct {
	Token        string    `json:"token"`
	Channel      string    `json:"channel"`
	ExpiresAt    time.Time `json:"expires_at"`
	RefreshAfter int       `json:"refresh_after_seconds"`
	IngestURL    string    `json:"ingest_url"`
	Rungs        []string  `json:"rungs"`
	MaxTopKbps   int       `json:"max_top_kbps"`
}

// CloudError is a refusal the cloud sent (its status and error code).
type CloudError struct {
	Status int
	Code   string
	Limit  string
	Value  int
}

func (e *CloudError) Error() string {
	return fmt.Sprintf("the cloud answered %d %s", e.Status, e.Code)
}

// ErrUnreachable is the agent or the cloud not answering.
var ErrUnreachable = errors.New("broadcast: Maktaba Cloud is unreachable")

// Agent is the relay agent's loopback admin port (MAKTABA_CLOUDLINK_ADMIN): it adds the
// home's server secret, which the API never holds.
type Agent struct {
	Addr   string
	Client *http.Client
}

func (a *Agent) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+a.Addr+"/admin/cloud-link/broadcast/"+path, rd)
	if err != nil {
		return err
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cl := a.Client
	if cl == nil {
		cl = &http.Client{Timeout: 25 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusBadGateway {
		return ErrUnreachable
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
			Limit string `json:"limit"`
			Value int    `json:"value"`
		}
		_ = json.Unmarshal(raw, &e)
		return &CloudError{Status: resp.StatusCode, Code: e.Error, Limit: e.Limit, Value: e.Value}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// State is the home's publisher state: its plan's limits and the agreement.
func (a *Agent) State(ctx context.Context) (Plan, error) {
	var p Plan
	err := a.do(ctx, http.MethodGet, "state", nil, &p)
	return p, err
}

// Publish creates the channel on the cloud.
func (a *Agent) Publish(ctx context.Context, req PublishRequest) (CloudChannel, error) {
	var c CloudChannel
	err := a.do(ctx, http.MethodPost, "channels", req, &c)
	return c, err
}

// Unpublish takes the channel off the cloud (a missing one is already off).
func (a *Agent) Unpublish(ctx context.Context, cloudID string) error {
	err := a.do(ctx, http.MethodDelete, "channels/"+cloudID, nil, nil)
	var ce *CloudError
	if errors.As(err, &ce) && ce.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// Revocations is the signed broadcast revocation list (50.25), as the cloud sent it.
func (a *Agent) Revocations(ctx context.Context) (string, error) {
	return a.text(ctx, "revocations", "revocations")
}

// IngestToken asks for the channel's ingest token (valid 24 h, asked again hourly). geo is
// the channel's country rule (story 50.23 §5, nil: none): the cloud signs it into the token
// with Maktaba's deny list, and the edge checks it at play and at every playlist.
func (a *Agent) IngestToken(ctx context.Context, channel, cloudID string, rungs []string, geo *Geo) (IngestToken, error) {
	var t IngestToken
	if cloudID == "" {
		return t, ErrNoCloudID // the cloud refuses a token without it (400): don't ask
	}
	body := map[string]any{"channel": channel, "cloud_id": cloudID, "rungs": rungs}
	if geo != nil {
		body["geo"] = geo
	}
	err := a.do(ctx, http.MethodPost, "ingest-token", body, &t)
	return t, err
}

// SpeedTestToken asks for a speed-test token (10 minutes, the speed test only).
func (a *Agent) SpeedTestToken(ctx context.Context, channel string) (IngestToken, error) {
	var t IngestToken
	err := a.do(ctx, http.MethodPost, "ingest-token", map[string]any{"channel": channel, "purpose": "speedtest"}, &t)
	return t, err
}

// Takedowns is this home's signed takedown instructions (story 50.24), as the cloud sent them.
func (a *Agent) Takedowns(ctx context.Context) (string, error) {
	return a.text(ctx, "takedowns", "takedowns")
}

// text asks a route whose answer is a signed text (the revocation list, the instructions).
func (a *Agent) text(ctx context.Context, path, code string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+a.Addr+"/admin/cloud-link/broadcast/"+path, nil)
	if err != nil {
		return "", err
	}
	cl := a.Client
	if cl == nil {
		cl = &http.Client{Timeout: 25 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode != http.StatusOK {
		return "", &CloudError{Status: resp.StatusCode, Code: code}
	}
	return string(raw), nil
}

// AckTakedown answers one instruction.
func (a *Agent) AckTakedown(ctx context.Context, id string, ack TakedownAck) error {
	return a.do(ctx, http.MethodPost, "takedowns/"+id+"/ack", ack, nil)
}

// Moderation is the home's moderation records: its statements, the reports about its content and
// its publisher's standing (the cloud's JSON, passed on).
func (a *Agent) Moderation(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	err := a.do(ctx, http.MethodGet, "moderation", nil, &raw)
	return raw, err
}

// CounterNotice files a counter-notice; the cloud's answer (or its refusal) is passed on.
func (a *Agent) CounterNotice(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	var raw json.RawMessage
	err := a.do(ctx, http.MethodPost, "moderation/counter-notices", body, &raw)
	return raw, err
}
