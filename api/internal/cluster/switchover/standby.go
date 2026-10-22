package switchover

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Standby homes (specs/epics/37-node-replica, "Emergency promotion"): a full machine the
// admin marks may take over as the home when the home is gone for good. The home keeps a
// kit of its secrets and keys sealed to each one (gathered and sealed by its supervisor,
// sent over the machine channel), again whenever they change, and removes it when the
// mark is removed; it tells every machine where the standbys are, so that with the home
// gone they find the one that took over.

// StandbyEvery is how often the home looks at its standby homes; StandbyRefresh how old a
// standby's kit may get before it is sent again for the sessions and devices that ride
// along with it (its secrets unchanged).
const (
	StandbyEvery   = 2 * time.Minute
	StandbyRefresh = 6 * time.Hour
)

// ErrNotStandby: only an approved full machine can be a standby home.
var ErrNotStandby = errors.New("switchover: only an approved full machine can be a standby home")

// StandbyState is a machine as a standby home, for the Machines tab.
type StandbyState struct {
	NodeID  string `json:"node_id"`
	Name    string `json:"name"`
	Standby bool   `json:"standby"`
	// Can: it may be one (an approved full machine).
	Can bool `json:"can"`
	// KitAt and KitTerm: when it last confirmed the kit it holds, and the term of that kit.
	KitAt   *time.Time `json:"kit_at,omitempty"`
	KitTerm int64      `json:"kit_term,omitempty"`
}

// standbyKit is what a standby holds (cluster_nodes.standby_kit).
type standbyKit struct {
	Hash string    `json:"hash"`
	At   time.Time `json:"at"`
	Term int64     `json:"term"`
}

// standbyKitID is the id its kit is sealed under (the supervisor's switchover.StandbyKitID).
func standbyKitID(node string) string { return "standby:" + strings.ToLower(node) }

type standbyRow struct {
	id, name, pubkey, state, webURL string
	full, standby                   bool
	kit                             *standbyKit
}

func (o *Orchestrator) standbyRows(ctx context.Context) ([]standbyRow, error) {
	rows, err := o.DB.QueryContext(ctx, `SELECT id::text, name, node_pubkey, state, COALESCE(web_url, ''), full_node,
	    standby, standby_kit FROM cluster_nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []standbyRow
	for rows.Next() {
		var r standbyRow
		var kit []byte
		if err := rows.Scan(&r.id, &r.name, &r.pubkey, &r.state, &r.webURL, &r.full, &r.standby, &kit); err != nil {
			return nil, err
		}
		if len(kit) > 0 {
			var k standbyKit
			if json.Unmarshal(kit, &k) == nil {
				r.kit = &k
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Standbys are the machines as standby homes.
func (o *Orchestrator) Standbys(ctx context.Context) ([]StandbyState, error) {
	rows, err := o.standbyRows(ctx)
	if err != nil {
		return nil, err
	}
	var out []StandbyState
	for _, r := range rows {
		if r.state != "approved" && !r.standby {
			continue
		}
		s := StandbyState{NodeID: r.id, Name: r.name, Standby: r.standby, Can: r.state == "approved" && r.full}
		if r.kit != nil {
			at := r.kit.At
			s.KitAt, s.KitTerm = &at, r.kit.Term
		}
		out = append(out, s)
	}
	return out, nil
}

// SetStandby marks (on) or unmarks node as a standby home; its kit follows within a
// moment (KeepStandbys).
func (o *Orchestrator) SetStandby(ctx context.Context, node string, on bool) error {
	q := `UPDATE cluster_nodes SET standby = $2, updated_at = now() WHERE id = $1`
	if on {
		q += ` AND state = 'approved' AND full_node`
	}
	res, err := o.DB.ExecContext(ctx, q, node, on)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotStandby
	}
	o.wakeStandbys()
	return nil
}

func (o *Orchestrator) wakeStandbys() {
	o.mu.Lock()
	if o.standbyWake == nil {
		o.standbyWake = make(chan struct{}, 1)
	}
	ch := o.standbyWake
	o.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

// KeepStandbys keeps every standby's kit current, removes the kit of a machine that is no
// longer one, and tells every machine where the standbys are, every StandbyEvery (and
// at once when a mark changes), until ctx ends.
func (o *Orchestrator) KeepStandbys(ctx context.Context) {
	o.mu.Lock()
	if o.standbyWake == nil {
		o.standbyWake = make(chan struct{}, 1)
	}
	wake := o.standbyWake
	o.mu.Unlock()
	t := time.NewTicker(StandbyEvery)
	defer t.Stop()
	told := map[string]string{} // agent session -> the standbys it was told of
	for {
		if err := o.standbysOnce(ctx, told); err != nil && ctx.Err() == nil {
			o.log().Warn("switchover: keeping the standby homes' kits", "err", err, "event", "standby_kit_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-wake:
		}
	}
}

func (o *Orchestrator) standbysOnce(ctx context.Context, told map[string]string) error {
	if o.Supervisor == nil || o.Hub == nil {
		return nil
	}
	rows, err := o.standbyRows(ctx)
	if err != nil {
		return err
	}
	var urls, peers []string
	var first error
	for _, r := range rows {
		want := r.standby && r.state == "approved" && r.full
		if want && r.webURL != "" {
			urls = append(urls, strings.TrimRight(r.webURL, "/"))
		}
		if r.state == "approved" && r.webURL != "" {
			peers = append(peers, strings.TrimRight(r.webURL, "/"))
		}
		if o.Hub.MachineAgent(r.id) == "" {
			continue // off: its kit (or its removal) waits for it
		}
		var err error
		switch {
		case want:
			err = o.sendStandbyKit(ctx, r)
		case r.kit != nil:
			if _, err = o.command(ctx, r.id, "standby.forget", map[string]any{"id": standbyKitID(r.id)}, time.Minute); err == nil {
				_, err = o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET standby_kit = NULL WHERE id = $1`, r.id)
			}
		}
		if err != nil && first == nil {
			first = fmt.Errorf("%s: %w", r.name, err)
		}
	}
	sort.Strings(urls)
	sort.Strings(peers)
	o.tellStandbys(ctx, urls, peers, told)
	return first
}

// sendStandbyKit sends a standby its kit when it does not hold the current one (or holds
// one older than StandbyRefresh).
func (o *Orchestrator) sendStandbyKit(ctx context.Context, r standbyRow) error {
	have := ""
	if r.kit != nil && time.Since(r.kit.At) < StandbyRefresh {
		have = r.kit.Hash
	}
	if _, err := base64.StdEncoding.DecodeString(r.pubkey); err != nil {
		return fmt.Errorf("its public key: %w", err)
	}
	var kit struct {
		Hash      string `json:"hash"`
		Unchanged bool   `json:"unchanged"`
		Path      string `json:"path"`
		Term      int64  `json:"term"`
	}
	if err := o.Supervisor.Call(ctx, "standby-kit", map[string]any{"to_node": r.id, "to_pubkey": r.pubkey,
		"have": have}, &kit); err != nil {
		return err
	}
	if kit.Unchanged {
		return nil
	}
	id := standbyKitID(r.id)
	f, err := os.Open(kit.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := readChunks(f, func(i uint64, last bool, sealed []byte) error {
		_, err := o.command(ctx, r.id, "standby.kit", map[string]any{"id": id, "i": i, "last": last,
			"data": base64.StdEncoding.EncodeToString(sealed)}, time.Minute)
		return err
	}); err != nil {
		return err
	}
	if _, err := o.command(ctx, r.id, "standby.commit", map[string]any{"id": id, "hash": kit.Hash}, time.Minute); err != nil {
		return err
	}
	b, _ := json.Marshal(standbyKit{Hash: kit.Hash, At: time.Now().UTC(), Term: kit.Term})
	_, err = o.DB.ExecContext(ctx, `UPDATE cluster_nodes SET standby_kit = $2::jsonb WHERE id = $1`, r.id, string(b))
	if err == nil {
		o.log().Info("switchover: a standby home holds the home's current kit", "node", r.name, "term", kit.Term,
			"event", "standby_kit_sent")
	}
	return err
}

// tellStandbys tells every connected machine where the standby homes are, once per
// agent session and again when they change.
func (o *Orchestrator) tellStandbys(ctx context.Context, urls, peers []string, told map[string]string) {
	sum := sha256.Sum256([]byte(strings.Join(urls, "\n") + "\x00" + strings.Join(peers, "\n")))
	want := hex.EncodeToString(sum[:])
	live := map[string]bool{}
	for node, sid := range o.Hub.MachineAgents() {
		live[sid] = true
		if told[sid] == want {
			continue
		}
		b, _ := json.Marshal(map[string]any{"urls": urls, "peers": peers})
		res, err := o.Hub.CommandWithin(ctx, sid, "home.standbys", b, "switchover", 30*time.Second)
		if err == nil && res.OK {
			told[sid] = want
		} else if ctx.Err() == nil {
			o.log().Debug("switchover: a machine did not take where the standby homes are", "node_id", node,
				"err", err, "event", "standby_list_not_taken")
		}
	}
	for sid := range told {
		if !live[sid] {
			delete(told, sid)
		}
	}
}
