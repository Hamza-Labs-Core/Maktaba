package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// Verify checks the app's server after a move, with the app's settings cfg:
// its API answers and every process its supervisor runs is up (an on-demand
// one nobody asked for may be down), retried for up to wait. On success the
// move is verified (Finish then removes the old task).
func Verify(ctx context.Context, tgt Target, cfg config.Config, wait time.Duration) error {
	rec, ok, err := ReadRecord(tgt.StateDir)
	if err != nil {
		return err
	}
	switch {
	case !ok:
		return errors.New("no move is recorded on this PC")
	case rec.Stage == StageVerified || rec.Stage == StageFinished:
		return nil
	case rec.Stage != StageMoved:
		return fmt.Errorf("the move did not get that far (%s): run it again", rec.Stage)
	}
	deadline := time.Now().Add(wait)
	for {
		err = serverUp(ctx, cfg)
		if err == nil || time.Now().After(deadline) {
			break
		}
		if !sleepCtx(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
	if err != nil {
		return fmt.Errorf("not yet: %w", err)
	}
	rec.Stage, rec.VerifiedAt = StageVerified, now()
	return writeRecord(tgt.StateDir, rec)
}

var verifyClient = &http.Client{Timeout: 5 * time.Second}

func serverUp(ctx context.Context, cfg config.Config) error {
	api := "http://" + supervisor.LocalAddr(cfg.Server.Listen, "8080") + "/healthz"
	if err := get(ctx, api, nil); err != nil {
		return fmt.Errorf("the API does not answer: %w", err)
	}
	if strings.TrimSpace(cfg.Supervisor.AdminAddr) == "" {
		return nil // the supervisor serves no state on this install
	}
	var snap struct {
		Processes []supervisor.ChildStatus `json:"processes"`
	}
	if err := get(ctx, "http://"+supervisor.LocalAddr(cfg.Supervisor.AdminAddr, "9103")+"/processes", &snap); err != nil {
		return fmt.Errorf("the supervisor does not answer: %w", err)
	}
	var down []string
	for _, c := range snap.Processes {
		switch {
		case c.State == supervisor.StateRunning && !c.CrashLoop:
		case (c.State == supervisor.StateExited || c.State == supervisor.StateStopped) && !c.Wanted:
		default:
			name := string(c.Role)
			if c.Instance != "" {
				name += " " + c.Instance
			}
			down = append(down, name+" ("+string(c.State)+")")
		}
	}
	if len(snap.Processes) == 0 {
		return errors.New("the supervisor runs nothing yet")
	}
	if len(down) > 0 {
		return errors.New("not running: " + strings.Join(down, ", "))
	}
	return nil
}

func get(ctx context.Context, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := verifyClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answers %s", url, resp.Status)
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
