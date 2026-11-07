// Package directjobs is the worker's part of direct remote access (Epic 50 story 50.15): it
// issues the certificates homes asked for (their CSRs, by DNS-01 against the responder) and
// checks from outside that each home is reachable, every 15 minutes or when its admin asks.
// The relays hand the results to the homes over their tunnels.
package directjobs

import (
	"context"
	"log/slog"
	"net/netip"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/direct"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Issuer orders a certificate (direct.Issuer in production).
type Issuer interface {
	Issue(ctx context.Context, csr []byte, homeID, domain, ca string) ([]byte, time.Time, error)
}

// Checker checks a home from outside (direct.Checker in production).
type Checker interface {
	Check(ctx context.Context, ip netip.Addr, port int, name, serverID string) string
}

// Jobs runs both.
type Jobs struct {
	Store      *stores.Direct
	Issuer     Issuer
	Checker    Checker
	CheckEvery time.Duration
	Logger     *slog.Logger
	// Overflow is told when a first certificate came from an overflow CA (an alert).
	Overflow func(domain, ca string)
}

// IssueOnce orders the pending CSRs. A home's first certificate tries its CA, then the next
// overflow CA if that one refuses; a renewal stays with the CA it came from.
func (j *Jobs) IssueOnce(ctx context.Context) int {
	pending, err := j.Store.PendingCSRs(ctx, 20)
	if err != nil {
		j.Logger.Warn("direct: pending CSRs", "err", err)
		return 0
	}
	done := 0
	for _, c := range pending {
		ca := c.CA
		for {
			chain, notAfter, err := j.Issuer.Issue(ctx, c.CSR, c.HomeID, c.Domain, ca)
			if err == nil {
				if err := j.Store.SetIssued(ctx, c.ServerID, ca, string(chain), notAfter); err != nil {
					j.Logger.Warn("direct: keeping a certificate", "server_id", c.ServerID, "err", err)
					break
				}
				if ca != direct.CALetsEncrypt && !c.IssuedAt.Valid && j.Overflow != nil {
					j.Overflow(c.Domain, ca)
				}
				j.Logger.Info("direct: certificate issued", "server_id", c.ServerID, "domain", c.Domain, "ca", ca,
					"not_after", notAfter, "event", "direct_cert_issued")
				done++
				break
			}
			next := direct.NextCA(ca)
			if c.IssuedAt.Valid || next == "" {
				_ = j.Store.SetIssueError(ctx, c.ServerID, err.Error())
				j.Logger.Warn("direct: issuing failed", "server_id", c.ServerID, "ca", ca, "err", err, "event", "direct_cert_failed")
				break
			}
			j.Logger.Warn("direct: a CA refused; the next", "server_id", c.ServerID, "ca", ca, "next", next, "err", err)
			ca = next
		}
	}
	return done
}

// CheckOnce checks the homes that are due.
func (j *Jobs) CheckOnce(ctx context.Context) int {
	every := j.CheckEvery
	if every <= 0 {
		every = 15 * time.Minute
	}
	due, err := j.Store.DueChecks(ctx, every, 50)
	if err != nil {
		j.Logger.Warn("direct: due checks", "err", err)
		return 0
	}
	for _, d := range due {
		result := j.Checker.Check(ctx, d.IP, d.Port, direct.Name(d.IP, d.HomeID, d.Domain), d.ServerID)
		if err := j.Store.SetCheck(ctx, d.ServerID, result, d.Port); err != nil {
			j.Logger.Warn("direct: keeping a check", "server_id", d.ServerID, "err", err)
		}
	}
	return len(due)
}

// Run issues and checks every 30 s until ctx ends.
func (j *Jobs) Run(ctx context.Context) {
	tk := time.NewTicker(30 * time.Second)
	defer tk.Stop()
	for {
		j.IssueOnce(ctx)
		j.CheckOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}
