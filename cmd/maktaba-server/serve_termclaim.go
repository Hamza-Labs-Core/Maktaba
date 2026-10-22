package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/switchover"
)

// An emergency's term claim (specs/epics/37-node-replica, "Emergency promotion"): two
// standby homes that each find the home gone would each take the next term and both be
// the home. Before it promotes, a standby claims the term at every other standby and every
// machine it knows (node.json's standby_homes and peers): one that answers as the home of
// that term or a newer one refuses it outright, and a machine is sent the claim sealed
// from the standby's key to its own, and seals its answer back (both keys in the copies'
// machines), so nothing on the way forges either. A machine keeps the first standby that
// claimed a term (for termClaimKeep; its own claim while it takes over, termClaimOwn) and
// refuses another, or any claim for a term no newer than its own home's. The standby
// takes over only if no machine it reached refused: two at once refuse each other and
// withdraw their own claim, to be asked again; one cut off from all the others is the
// partition the warning before an emergency names.

const (
	termClaimInfo  = "maktaba-term-claim"
	termAnswerInfo = "maktaba-term-claim-answer"
	termClaimsFile = "term-claims.json"
	// termClaimKeep is how long another standby's claim stands here (it takes over in
	// that time, and its machines follow it, or it never will); termClaimOwn this
	// machine's own while it takes over (the promotion's own limit and more)
	termClaimKeep = 10 * time.Minute
	termClaimOwn  = time.Hour
)

// termClaimed is a term's claim kept here.
type termClaimed struct {
	Claimant string    `json:"claimant"`
	At       time.Time `json:"at"`
}

// termClaimMsg is a claim as its standby seals it, to the machine it names.
type termClaimMsg struct {
	Term int64  `json:"term"`
	From string `json:"from"`
	To   string `json:"to"`
}

// termAnswer is a machine's answer to a claim, sealed back to the standby: who holds the
// term ("": the claimant now), and the term of this machine's own home.
type termAnswer struct {
	Term      int64  `json:"term"`
	To        string `json:"to"`
	ClaimedBy string `json:"claimed_by,omitempty"`
	HomeTerm  int64  `json:"home_term"`
}

func (s *switching) claimsPath() string {
	return filepath.Join(s.cfg.StateDir(), "switchover", termClaimsFile)
}

// claimTerm records claimant's claim on term here unless a claim of another stands: who
// holds it then ("": claimant does now). Claims past their time go.
func (s *switching) claimTerm(term int64, claimant, self string, now time.Time) (string, error) {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	claims := map[int64]termClaimed{}
	if b, err := os.ReadFile(s.claimsPath()); err == nil {
		_ = json.Unmarshal(b, &claims)
	}
	for t, c := range claims {
		keep := termClaimKeep
		if c.Claimant == self {
			keep = termClaimOwn
		}
		if now.Sub(c.At) > keep {
			delete(claims, t)
		}
	}
	if c, ok := claims[term]; ok && c.Claimant != claimant {
		return c.Claimant, nil
	}
	claims[term] = termClaimed{Claimant: claimant, At: now.UTC()}
	b, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return "", writePrivate(s.claimsPath(), b)
}

// withdrawClaim forgets this machine's own claim on term (it did not take over).
func (s *switching) withdrawClaim(term int64, self string) {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	claims := map[int64]termClaimed{}
	if b, err := os.ReadFile(s.claimsPath()); err != nil || json.Unmarshal(b, &claims) != nil {
		return
	}
	if claims[term].Claimant != self {
		return
	}
	delete(claims, term)
	if b, err := json.Marshal(claims); err == nil {
		_ = writePrivate(s.claimsPath(), b)
	}
}

// machineKey is an approved machine's public key in db (a standby home's when standby).
func machineKey(ctx context.Context, db *sql.DB, id string, standby bool) ([]byte, error) {
	var pub string
	var isStandby bool
	err := db.QueryRowContext(ctx, `SELECT node_pubkey, standby FROM cluster_nodes WHERE id = $1::uuid AND state = 'approved'`,
		id).Scan(&pub, &isStandby)
	if err != nil {
		return nil, fmt.Errorf("no approved machine %s: %w", id, err)
	}
	if standby && !isStandby {
		return nil, fmt.Errorf("machine %s is not a standby home", id)
	}
	return base64.StdEncoding.DecodeString(pub)
}

// machineKeys finds an approved machine's public key (a standby home's when standby):
// in this machine's copy (machineKey), or a test's.
type machineKeys func(ctx context.Context, id string, standby bool) ([]byte, error)

// copyKeys is machineKeys over this machine's copy; done closes it.
func (s *switching) copyKeys() (keys machineKeys, done func(), err error) {
	db, err := s.copyDB()
	if err != nil {
		return nil, nil, err
	}
	return func(ctx context.Context, id string, standby bool) ([]byte, error) {
		return machineKey(ctx, db, id, standby)
	}, func() { _ = db.Close() }, nil
}

// termClaim answers another standby's claim (the API's POST /api/cluster/term-claim on a
// machine): answerClaim, as this machine (node.json) with its copy's keys.
func (s *switching) termClaim(ctx context.Context, req homeRequest) (any, error) {
	keys, done, err := s.copyKeys()
	if err != nil {
		return nil, err
	}
	defer done()
	return s.answerClaim(ctx, req, supervisor.NodeConfig(), keys)
}

// answerClaim answers a claim as machine n: it opens only from that standby to this
// machine, and is refused once another holds the term, or this machine's home is of it or
// newer.
func (s *switching) answerClaim(ctx context.Context, req homeRequest, n config.NodeConfig, keys machineKeys) (any, error) {
	from, err := keys(ctx, req.From, true)
	if err != nil {
		return nil, err
	}
	priv, err := base64.StdEncoding.DecodeString(n.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("this machine's key: %w", err)
	}
	salt := fmt.Sprintf("term-%d", req.Term)
	raw, err := switchover.OpenPair(priv, from, salt, termClaimInfo, req.Sealed)
	if err != nil {
		return nil, err
	}
	var m termClaimMsg
	if json.Unmarshal(raw, &m) != nil || m.Term != req.Term || m.From != req.From || m.To != n.NodeID {
		return nil, errors.New("a claim that is not what it says")
	}
	a := termAnswer{Term: m.Term, To: m.From, HomeTerm: n.Home.Term}
	if a.HomeTerm < m.Term {
		if a.ClaimedBy, err = s.claimTerm(m.Term, m.From, n.NodeID, time.Now()); err != nil {
			return nil, err
		}
	}
	b, _ := json.Marshal(a)
	sealed, err := switchover.SealPair(priv, from, salt, termAnswerInfo, b)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sealed": sealed}, nil
}

// claimEverywhere claims term for this standby before an emergency promotes it:
// claimAll, as this machine with its copy's keys.
func (s *switching) claimEverywhere(ctx context.Context, term int64) error {
	n := supervisor.NodeConfig()
	keys, done, err := s.copyKeys()
	if err != nil {
		return err
	}
	defer done()
	return s.claimAll(ctx, term, n, keys)
}

// claimAll claims term for machine n here and at every other machine it knows, all at
// once: an error names one that refused (another standby holds the term, or it is or
// follows a home of it or newer); n's own claim is withdrawn then, so it can be asked
// again.
func (s *switching) claimAll(ctx context.Context, term int64, n config.NodeConfig, keys machineKeys) error {
	if holder, err := s.claimTerm(term, n.NodeID, n.NodeID, time.Now()); err != nil {
		return err
	} else if holder != "" {
		return fmt.Errorf("another standby home (%s) is taking over as the home: this machine follows it once it is", holder)
	}
	priv, err := base64.StdEncoding.DecodeString(n.PrivateKey)
	if err != nil {
		s.withdrawClaim(term, n.NodeID)
		return fmt.Errorf("this machine's key: %w", err)
	}
	var urls []string
	seen := map[string]bool{}
	for _, u := range append(append([]string(nil), n.StandbyHomes...), n.Peers...) {
		if u = strings.TrimRight(u, "/"); u != "" && !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	why := make([]string, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			why[i] = claimAt(ctx, keys, priv, n.NodeID, u, term)
		}(i, u)
	}
	wg.Wait()
	for _, w := range why {
		if w != "" {
			s.withdrawClaim(term, n.NodeID)
			return errors.New(w)
		}
	}
	return nil
}

// claimAt claims term at the machine at base: why it refused ("": it did not, it did not
// answer, or it answered nothing that opens: a machine cut off counts for nothing).
func claimAt(ctx context.Context, keys machineKeys, priv []byte, self, base string, term int64) string {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	var who struct {
		Role string `json:"role"`
		ID   string `json:"id"`
		Term int64  `json:"term"`
	}
	if getJSON(cctx, client, base+"/api/cluster/self", &who) != nil || who.ID == self {
		return ""
	}
	if who.Role == "home" {
		if who.Term >= term {
			return fmt.Sprintf("%s is the home already (term %d): this machine follows it once it hears of it", base, who.Term)
		}
		return ""
	}
	pub, err := keys(cctx, who.ID, false)
	if err != nil {
		return ""
	}
	salt := fmt.Sprintf("term-%d", term)
	msg, _ := json.Marshal(termClaimMsg{Term: term, From: self, To: who.ID})
	sealed, err := switchover.SealPair(priv, pub, salt, termClaimInfo, msg)
	if err != nil {
		return ""
	}
	body, _ := json.Marshal(map[string]any{"from": self, "term": term, "sealed": sealed})
	var out struct {
		Sealed string `json:"sealed"`
	}
	if postJSON(cctx, client, base+"/api/cluster/term-claim", body, &out) != nil {
		return ""
	}
	raw, err := switchover.OpenPair(priv, pub, salt, termAnswerInfo, out.Sealed)
	var a termAnswer
	if err != nil || json.Unmarshal(raw, &a) != nil || a.Term != term || a.To != self {
		return ""
	}
	switch {
	case a.HomeTerm >= term:
		return fmt.Sprintf("%s follows a home of term %d already: this machine follows it once it hears of it", base, a.HomeTerm)
	case a.ClaimedBy != "" && a.ClaimedBy != self:
		return fmt.Sprintf("another standby home (%s) is taking over as the home (%s says so): this machine follows it once it is",
			a.ClaimedBy, base)
	}
	return ""
}

// postJSON posts body to u and decodes a 200's answer into out.
func postJSON(ctx context.Context, client *http.Client, u string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(out)
}
