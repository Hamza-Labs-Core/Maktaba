package main

// A machine its home no longer takes goes back to the join flow by itself, as itself: when
// the home refuses its agent's sessions again and again as not a member (4003: it removed
// or rejected the machine) or revoked (4010), or answers its request for its database
// access that it does not know it, the machine asks the home to take it again with its own
// node id and key (machineagent.Rejoin: never a new identity), logs the pairing code for
// the home's admin, who approves it on the Machines page, and keeps the credentials the
// home seals to it (node.json), then starts again. A home that still holds the machine
// (revoked: already_member) is asked again later, its answer logged: an admin removes the
// machine there first.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/machineagent"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
)

// rejoinAfter is how many refused sessions in a row, over at least rejoinSpan, send the
// machine back to the join flow (one refusal can meet a home in the middle of a switch);
// rejoinRetry is how soon it asks again after a refusal, twice as long each time up to
// rejoinRetryMax; rejoinPoll how often it polls the home for the admin's answer.
var (
	rejoinAfter    = 3
	rejoinSpan     = time.Minute
	rejoinRetry    = 5 * time.Minute
	rejoinRetryMax = time.Hour
	rejoinPoll     = 5 * time.Second
)

// rejoinWatch counts the home's refusals and runs the re-join.
type rejoinWatch struct {
	// node is this machine's node.json (tests replace it); clock is now
	node  func() config.NodeConfig
	clock func() time.Time
	// joined keeps the home's credentials (node.json) and starts this machine again
	// (tests replace it)
	joined func(n config.NodeConfig, j machineagent.Joined) error

	mu       sync.Mutex
	refusals int
	first    time.Time
	cancel   context.CancelFunc // the re-join running, if one is
	state    *machineagent.RejoinState
}

func newRejoinWatch() *rejoinWatch {
	return &rejoinWatch{node: supervisor.NodeConfig, clock: time.Now, joined: keepRejoined}
}

// refused is a session the home ended as one it does not take.
func (w *rejoinWatch) refused(ctx context.Context, code int) {
	if w == nil {
		return
	}
	now := w.clock()
	w.mu.Lock()
	if w.refusals == 0 {
		w.first = now
	}
	w.refusals++
	start := w.cancel == nil && w.refusals >= rejoinAfter && now.Sub(w.first) >= rejoinSpan
	var rctx context.Context
	if start {
		rctx, w.cancel = context.WithCancel(ctx)
	}
	w.mu.Unlock()
	if start {
		why := "it does not take this machine as a member (removed, or rejected)"
		if code == 4010 {
			why = "it revoked this machine"
		}
		fmt.Fprintf(os.Stderr, "[serve] node: the home refuses this machine's sessions: %s; asking it to take this "+
			"machine again, as itself (its own id and key), for its admin to approve\n", why)
		go w.run(rctx)
	}
}

// unknown is the home answering this machine's request for its access that it does not
// know it, or that it is not approved: the join flow at once.
func (w *rejoinWatch) unknown(ctx context.Context) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.refusals, w.first = rejoinAfter, w.clock().Add(-rejoinSpan)
	w.mu.Unlock()
	w.refused(ctx, 4003)
}

// connected is a session the home took: no re-join (one running stops: the home takes
// this machine as it is; a request it made expires on the home).
func (w *rejoinWatch) connected() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.refusals = 0
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	w.state = nil
	w.mu.Unlock()
}

// State is the re-join for the machine agent's local state (nil: none).
func (w *rejoinWatch) State() *machineagent.RejoinState {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state == nil {
		return nil
	}
	c := *w.state
	return &c
}

func (w *rejoinWatch) set(st machineagent.RejoinState) {
	st.At = w.clock().UTC()
	w.mu.Lock()
	w.state = &st
	w.mu.Unlock()
}

// run asks the home to take this machine again until it does (or ctx ends).
func (w *rejoinWatch) run(ctx context.Context) {
	wait := rejoinRetry
	for ctx.Err() == nil {
		n := w.node()
		r := machineagent.Rejoin{HomeURL: n.HomeURL(), NodeID: n.NodeID, Name: n.Name, PrivateKey: n.PrivateKey,
			CertDir: filepath.Dir(config.LocalNodeConfigPath()), Version: version.Version}
		err := w.once(ctx, n, r)
		if err == nil || ctx.Err() != nil {
			return
		}
		var refused *machineagent.RejoinRefused
		switch {
		case errors.As(err, &refused) && refused.Code == "already_member":
			w.set(machineagent.RejoinState{State: "held", Reason: refused.Error()})
			fmt.Fprintf(os.Stderr, "[serve] node: the home still holds this machine (revoked): an admin removes it on "+
				"the home's Machines page, then this machine asks again by itself (in %s)\n", wait)
		default:
			w.set(machineagent.RejoinState{State: "refused", Reason: err.Error()})
			fmt.Fprintf(os.Stderr, "[serve] node: the home did not take this machine again: %v; asking again in %s\n", err, wait)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, rejoinRetryMax)
	}
}

// once is one request: asked, its pairing code logged, the admin's answer waited for.
func (w *rejoinWatch) once(ctx context.Context, n config.NodeConfig, r machineagent.Rejoin) error {
	p, err := r.Ask(ctx)
	if err != nil {
		return err
	}
	code := machineagent.FormatCode(p.Code)
	w.set(machineagent.RejoinState{State: "waiting", Code: code, Home: p.HomeName})
	fmt.Fprintf(os.Stderr, "[serve] node: asked %s to take this machine (%s) again. Pairing code: %s. On the home, open "+
		"Admin > Machines, find %s and enter this code.\n", homeLabel(p.HomeName, n.HomeURL()), n.Name, code, n.Name)
	j, err := p.Wait(ctx, rejoinPoll)
	if err != nil {
		return err
	}
	if err := w.joined(n, j); err != nil {
		return err
	}
	w.set(machineagent.RejoinState{State: "joined", Home: p.HomeName})
	fmt.Fprintln(os.Stderr, "[serve] node: the home took this machine again (approved): its new access kept, starting again")
	return nil
}

func homeLabel(name, url string) string {
	if name != "" {
		return name
	}
	return url
}

// keepRejoined keeps the home's credentials in node.json (its database access, the home's
// address and keys; this machine's id, key and everything else as they are) and starts
// this machine again with them.
func keepRejoined(n config.NodeConfig, j machineagent.Joined) error {
	err := updateNodeConfig(n.NodeID, func(c *config.NodeConfig) {
		c.DatabaseURL, c.DatabaseCA, c.DatabaseTLS = j.DatabaseURL, j.DatabaseCA, j.DatabaseTLS
		if j.Home.APIURL != "" {
			c.Home.URL = j.Home.APIURL
		}
		if j.Home.ClusterPub != "" {
			c.Home.ClusterPub = j.Home.ClusterPub
		}
		if j.Home.InstanceID != "" {
			c.Home.InstanceID = j.Home.InstanceID
		}
		if j.Home.Name != "" {
			c.Home.Name = j.Home.Name
		}
	})
	if err != nil {
		return err
	}
	requestReload()
	return nil
}
