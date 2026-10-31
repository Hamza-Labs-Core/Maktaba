package main

// The home's update moves its TV to the new release's streaming binary (Epic 35 story
// 35.6): updater.TVSwapper over the API's change controller, asked on its loopback port
// with this supervisor's per-boot local token (POST /internal/updates/tv-change, GET …/
// {run}, POST …/{run}/withdraw).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// apiTV is updater.TVSwapper over this machine's API.
type apiTV struct {
	cfg        config.Config
	releaseDir string
	http       *http.Client
}

func (t *apiTV) client() *http.Client {
	if t.http != nil {
		return t.http
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (t *apiTV) base() string {
	return "http://" + supervisor.LocalAddr(t.cfg.Server.Listen, ports.APIPublic.PortString()) + "/internal/updates/tv-change"
}

// Begin implements updater.TVSwapper: the run's change of this release's streaming binary.
func (t *apiTV) Begin(ctx context.Context, j *updater.Journal) (updater.TVStatus, error) {
	v, sum, err := t.Binary(j)
	if err != nil {
		return updater.TVStatus{}, err
	}
	return t.do(ctx, http.MethodPost, "", map[string]string{"run_id": j.Job.RunID, "version": v, "sha256": sum})
}

// Binary implements updater.TVBinary: the release's streaming binary, as the change carries
// it.
func (t *apiTV) Binary(j *updater.Journal) (string, string, error) {
	// the binary's own spelling of its version (a release build says "v1.5.0"): what the
	// front door and the slots compare a TV release's binary with
	v := j.Job.Version
	if layout.CompareVersions(version.Version, j.Job.Version) == 0 {
		v = version.Version
	}
	rb, err := supervisor.ReleaseStreaming(supervisor.Options{Config: t.cfg, ReleaseDir: t.releaseDir}, v)
	if err != nil {
		return "", "", fmt.Errorf("the release's streaming binary: %w", err)
	}
	return v, rb.SHA256, nil
}

// Status implements updater.TVSwapper.
func (t *apiTV) Status(ctx context.Context, j *updater.Journal) (updater.TVStatus, error) {
	return t.do(ctx, http.MethodGet, "/"+j.Job.RunID, nil)
}

// Withdraw implements updater.TVSwapper.
func (t *apiTV) Withdraw(ctx context.Context, j *updater.Journal, reason string) error {
	_, err := t.do(ctx, http.MethodPost, "/"+j.Job.RunID+"/withdraw", map[string]string{"reason": reason})
	return err
}

func (t *apiTV) do(ctx context.Context, method, path string, body any) (updater.TVStatus, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return updater.TVStatus{}, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.base()+path, rd)
	if err != nil {
		return updater.TVStatus{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	tok, _ := os.ReadFile(supervisor.LocalTokenPath(t.cfg))
	req.Header.Set("X-Maktaba-Local-Token", strings.TrimSpace(string(tok)))
	resp, err := t.client().Do(req)
	if err != nil {
		return updater.TVStatus{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return updater.TVStatus{}, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var st updater.TVStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return updater.TVStatus{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return st, nil
}
