package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// Verify checks the app's server after a move, with the app's settings cfg:
// its API answers and every process its supervisor runs is up (an on-demand
// one nobody asked for may be down), retried for up to wait; the app's server
// listens where other devices reached the old install, beyond loopback
// (checkReach); and the firewall rules the move made for the app's
// programs are there, on, and cover the network in use, so other devices reach
// it as they reached the old install.
// On success the move is verified (Finish then removes the old task).
func Verify(ctx context.Context, sys Sys, tgt Target, cfg config.Config, wait time.Duration) error {
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
		if err == nil {
			err = checkReach(sys, rec.Source, tgt)
		}
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
	if err := checkFirewall(sys, rec.Firewall); err != nil {
		return fmt.Errorf("not yet: %w", err)
	}
	rec.Stage, rec.VerifiedAt = StageVerified, now()
	return writeRecord(tgt.StateDir, rec)
}

// reachedOn are the old install's settings other devices and the home reach
// it on (its server.toml's, carried into the app's): its web app, API and
// streaming.
var reachedOn = []string{"web", "api", "stream"}

// checkReach: every address other devices reached the old install on (one of
// reachedOn, bound beyond loopback, as 0.0.0.0:8780) is the app's server's now:
// a program of the app listens on its port, bound beyond loopback too (the
// address itself or every one). /healthz on loopback proves nothing of that.
func checkReach(sys Sys, src Source, tgt Target) error {
	var procs []Proc
	for _, key := range reachedOn {
		host, port, err := net.SplitHostPort(src.Ports[key])
		if err != nil || port == "" || loopbackHost(host) {
			continue
		}
		if procs == nil {
			if procs, err = sys.Processes(); err != nil {
				return fmt.Errorf("list the running programs: %w", err)
			}
		}
		ls, err := sys.Listeners(port)
		if err != nil {
			return err
		}
		ok := false
		for _, l := range ls {
			ok = ok || (!loopbackHost(l.Addr) && (anyHost(host) || anyHost(l.Addr) || sameHost(l.Addr, host)) && tgt.appListens(procs, src, l.PID))
		}
		if !ok {
			return fmt.Errorf("the app's server does not listen on %s for other devices, where they reached the old install (its %s address)",
				src.Ports[key], key)
		}
	}
	return nil
}

// appListens: the process pid is one of the app's programs (in its payload;
// without one known, a program that is not the old install's).
func (t Target) appListens(procs []Proc, src Source, pid int) bool {
	for _, p := range procs {
		if p.PID != pid || p.Exe == "" {
			continue
		}
		if t.AppPayload != "" {
			return within(p.Exe, t.AppPayload)
		}
		return !src.owns(p)
	}
	return false
}

// loopbackHost: host reaches only this PC (localhost, 127.x, ::1).
func loopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// anyHost: host is every address of the PC ("", 0.0.0.0, ::).
func anyHost(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func sameHost(a, b string) bool {
	ia, ib := net.ParseIP(strings.Trim(a, "[]")), net.ParseIP(strings.Trim(b, "[]"))
	if ia != nil && ib != nil {
		return ia.Equal(ib)
	}
	return strings.EqualFold(strings.Trim(a, "[]"), strings.Trim(b, "[]"))
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
