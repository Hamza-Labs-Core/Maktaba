package cloudlink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Reporter tells the cloud which cloud accounts this home linked (by id
// only), through the relay agent on loopback (POST
// /admin/cloud-link/links; the agent holds the server secret the cloud
// wants). The cloud uses the list to show this home to those accounts;
// a stale list grants nothing, since this home decides every sign-in.
type Reporter struct {
	// AgentAddr is the agent's loopback admin address (host:port).
	AgentAddr string
	Links     interface {
		LinkedCloudUsers(ctx context.Context) ([]string, error)
	}
	Client *http.Client
	Log    *slog.Logger

	kick chan struct{}
}

// NewReporter makes a reporter (Run it; Kick it after a change).
func NewReporter(agentAddr string, links interface {
	LinkedCloudUsers(ctx context.Context) ([]string, error)
}, log *slog.Logger) *Reporter {
	return &Reporter{AgentAddr: agentAddr, Links: links, Log: log, kick: make(chan struct{}, 1)}
}

// Kick asks for a report soon (after a link change). Never blocks.
func (r *Reporter) Kick() {
	if r == nil || r.kick == nil {
		return
	}
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run reports at start, after each Kick, and every interval, retrying a
// failed report with backoff until ctx ends.
func (r *Reporter) Run(ctx context.Context, interval time.Duration) {
	r.Kick()
	retry := time.Duration(0)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		var wait <-chan time.Time
		if retry > 0 {
			wait = time.After(retry)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
		case <-t.C:
		case <-wait:
		}
		if err := r.report(ctx); err != nil {
			retry = min(max(2*retry, 5*time.Second), 5*time.Minute)
			if r.Log != nil {
				r.Log.Warn("cloud link: reporting the linked accounts to the cloud failed; retrying",
					"err", err, "retry_in", retry.String(), "event", "cloudlink_report_failed")
			}
			continue
		}
		retry = 0
	}
}

func (r *Reporter) report(ctx context.Context) error {
	ids, err := r.Links.LinkedCloudUsers(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string][]string{"cloud_user_ids": ids})
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, "http://"+r.AgentAddr+"/admin/cloud-link/links", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	cl := r.Client
	if cl == nil {
		cl = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the relay agent answered %d", resp.StatusCode)
	}
	return nil
}
