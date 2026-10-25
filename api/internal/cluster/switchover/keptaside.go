package switchover

import (
	"crypto/hkdf"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// What an old home wrote after the cut of the emergency that took the home from it: it
// reports it when it is back, sealed from the identity the standby's kit registered it
// with to the cluster's key (the supervisor's switchover.SealReport), before it becomes a
// machine. The admin downloads it, or applies what does not conflict (the supervisor
// plans and applies it: cmd/maktaba-server/internal/switchover/apply.go).

// reportInfo is the purpose the report is sealed for (switchover.ReportInfo).
const reportInfo = "maktaba-kept-aside"

// maxReport bounds a sealed report (an old home's carries at most 100,000 changes, its
// switchover.maxKeptChanges).
const maxReport = 64 << 20

// openReport opens an old home's sealed report of term with the cluster key's private
// half and the old home's public key.
func openReport(clusterPriv, oldPub []byte, term int64, sealed string) ([]byte, error) {
	salt := fmt.Sprintf("term-%d", term)
	shared, err := curve25519.X25519(clusterPriv, oldPub)
	if err != nil {
		return nil, err
	}
	key, err := hkdf.Key(sha256.New, shared, []byte(salt), reportInfo, chacha20poly1305.KeySize)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < aead.NonceSize() {
		return nil, errors.New("switchover: a report that is not one")
	}
	return aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(salt))
}

// KeptReport is a report as the Machines tab lists it.
type KeptReport struct {
	ID         string         `json:"id"`
	Term       int64          `json:"term"`
	FromName   string         `json:"from_name"`
	State      string         `json:"state"`
	Lost       bool           `json:"lost"`
	Received   time.Time      `json:"received_at"`
	AppliedAt  *time.Time     `json:"applied_at,omitempty"`
	Changes    int            `json:"changes"`
	Txs        int            `json:"transactions"`
	Counts     map[string]int `json:"counts"`
	AppliedTxs *int           `json:"applied_transactions,omitempty"`
	// Truncated: the old home wrote more than a report carries; Total is how much (Changes
	// is what the report holds; the rest is in the old home's database, set aside on it)
	Truncated bool `json:"truncated,omitempty"`
	Total     int  `json:"total,omitempty"`
}

// mountKept wires the reports' routes:
//
//	POST /api/cluster/kept-aside/report?node=&term=  the old home's sealed report (public)
//	GET  /api/cluster/kept-aside               the reports (admins)
//	GET  /api/cluster/kept-aside/{id}/download the report itself (JSON)
//	GET  /api/cluster/kept-aside/{id}/plan     what applying it would do now
//	POST /api/cluster/kept-aside/{id}/apply    {plan}: apply what that plan applies
//	POST /api/cluster/kept-aside/{id}/dismiss  set it aside, nothing applied
func (o *Orchestrator) mountKept(r chi.Router) {
	r.Post("/api/cluster/kept-aside/report", o.postReport)
	r.Get("/api/cluster/kept-aside", o.getReports)
	r.Get("/api/cluster/kept-aside/{id}/download", o.getReportFile)
	r.Get("/api/cluster/kept-aside/{id}/plan", o.getPlan)
	r.Post("/api/cluster/kept-aside/{id}/apply", o.postApply)
	r.Post("/api/cluster/kept-aside/{id}/dismiss", o.postDismiss)
}

// postReport takes an old home's sealed report: who sends it, and of which term, are in
// the address, so a machine that is not the old home that term's emergency replaced is
// refused before its body is read.
func (o *Orchestrator) postReport(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	term, terr := strconv.ParseInt(r.URL.Query().Get("term"), 10, 64)
	if _, err := uuid.Parse(node); err != nil || terr != nil || term < 1 {
		httperror.Write(w, r, httperror.BadRequest("node and term"))
		return
	}
	ctx := r.Context()
	// the old home: registered by the switch that began term (with the term, in one
	// transaction: an emergency, or a planned switch the old home called off without the
	// new home's word), not by an earlier or a later one
	// (or, for an emergency's term, another standby home that took the same term while cut
	// off from this one, and gave way to it: what it wrote since it took over)
	var pubB64, name string
	err := o.DB.QueryRowContext(ctx, `SELECT n.node_pubkey, n.name FROM cluster_nodes n
	    JOIN cluster_terms t ON t.term = $2 AND t.reason IN ('emergency', 'planned')
	    WHERE n.id = $1 AND n.state = 'approved' AND (
	      (n.approved_by = 'switchover' AND n.approved_at >= t.started_at
	       AND n.approved_at < COALESCE((SELECT min(started_at) FROM cluster_terms WHERE term > t.term), 'infinity'))
	      OR (n.standby AND t.reason = 'emergency' AND t.home_node IS DISTINCT FROM n.id))`,
		node, term).Scan(&pubB64, &name)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.Forbidden("", "not an old home of this cluster"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("kept aside").Wrap(err))
		return
	}
	var req struct {
		NodeID string `json:"node_id"`
		Term   int64  `json:"term"`
		Sealed string `json:"sealed"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxReport)).Decode(&req); err != nil ||
		req.NodeID != node || req.Term != term {
		httperror.Write(w, r, httperror.BadRequest("a sealed report"))
		return
	}
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	priv, perr := os.ReadFile(o.ClusterKeyFile)
	if err != nil || perr != nil || len(pub) != 32 || len(priv) != 32 {
		httperror.Write(w, r, httperror.Internal("kept aside").Wrap(err))
		return
	}
	plain, err := openReport(priv, pub, req.Term, req.Sealed)
	if err != nil {
		httperror.Write(w, r, httperror.Forbidden("", "a report that does not open"))
		return
	}
	var rep struct {
		ID       string          `json:"id"`
		Term     int64           `json:"term"`
		FromNode string          `json:"from_node"`
		FromName string          `json:"from_name"`
		Cut      string          `json:"cut_lsn"`
		Lost     bool            `json:"lost"`
		Aside    json.RawMessage `json:"aside"`
	}
	if err := json.Unmarshal(plain, &rep); err != nil || rep.FromNode != req.NodeID || rep.Term != req.Term {
		httperror.Write(w, r, httperror.BadRequest("the report is not that machine's of that term"))
		return
	}
	if _, err := uuid.Parse(rep.ID); err != nil {
		rep.ID = uuid.NewString()
	}
	if rep.FromName == "" {
		rep.FromName = name
	}
	if len(rep.Aside) == 0 || string(rep.Aside) == "null" {
		rep.Aside = json.RawMessage(`{}`)
	}
	if _, err := o.DB.ExecContext(ctx, `INSERT INTO cluster_kept_aside (id, term, from_node, from_name, cut_lsn, report, lost)
	    VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6::jsonb, $7) ON CONFLICT (term, from_node) DO NOTHING`,
		rep.ID, rep.Term, rep.FromNode, rep.FromName, rep.Cut, string(rep.Aside), rep.Lost); err != nil {
		httperror.Write(w, r, httperror.Internal("kept aside").Wrap(err))
		return
	}
	o.log().Info("switchover: the old home reported what it wrote after the cut", "from", rep.FromName,
		"term", rep.Term, "event", "kept_aside_reported")
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"received": true})
}

// Reports are the old homes' reports, newest first.
func (o *Orchestrator) Reports(r *http.Request) ([]KeptReport, error) {
	rows, err := o.DB.QueryContext(r.Context(), `SELECT id::text, term, from_name, state, lost, received_at, applied_at,
	    report, applied FROM cluster_kept_aside ORDER BY received_at DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KeptReport{}
	for rows.Next() {
		var k KeptReport
		var applied sql.NullTime
		var report, plan []byte
		if err := rows.Scan(&k.ID, &k.Term, &k.FromName, &k.State, &k.Lost, &k.Received, &applied, &report, &plan); err != nil {
			return nil, err
		}
		if applied.Valid {
			t := applied.Time
			k.AppliedAt = &t
		}
		var a struct {
			Transactions []struct {
				Changes []json.RawMessage `json:"changes"`
			} `json:"transactions"`
			Counts    map[string]int `json:"counts"`
			Truncated bool           `json:"truncated"`
			Total     int            `json:"total"`
		}
		_ = json.Unmarshal(report, &a)
		k.Txs, k.Counts, k.Truncated, k.Total = len(a.Transactions), a.Counts, a.Truncated, a.Total
		for _, t := range a.Transactions {
			k.Changes += len(t.Changes)
		}
		if len(plan) > 0 {
			var p struct {
				Apply int `json:"apply"`
			}
			if json.Unmarshal(plan, &p) == nil {
				k.AppliedTxs = &p.Apply
			}
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (o *Orchestrator) getReports(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	rs, err := o.Reports(r)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("kept aside").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"reports": rs})
}

func reportID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("report"))
		return "", false
	}
	return id, true
}

func (o *Orchestrator) getReportFile(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var term int64
	var from string
	var report []byte
	err := o.DB.QueryRowContext(r.Context(), `SELECT term, from_name, report FROM cluster_kept_aside WHERE id = $1`, id).
		Scan(&term, &from, &report)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("report"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("kept aside").Wrap(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="kept-aside-term-%d.json"`, term))
	_, _ = w.Write(report)
}

func (o *Orchestrator) getPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	id, ok := reportID(w, r)
	if !ok || !o.supervised(w, r) {
		return
	}
	var plan json.RawMessage
	if err := o.Supervisor.Call(r.Context(), "kept-plan", map[string]any{"report": id}, &plan); err != nil {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/kept-aside", Title: "no plan",
			Status: http.StatusConflict, Detail: err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(plan)
}

func (o *Orchestrator) postApply(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	id, ok := reportID(w, r)
	if !ok || !o.supervised(w, r) {
		return
	}
	var req struct {
		Plan string `json:"plan"`
	}
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	var out struct {
		Plan    json.RawMessage `json:"plan"`
		Changed bool            `json:"changed"`
	}
	if err := o.Supervisor.Call(r.Context(), "kept-apply", map[string]any{"report": id, "plan": req.Plan,
		"by": "user:" + p.UserID}, &out); err != nil {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/kept-aside", Title: "not applied",
			Status: http.StatusConflict, Detail: err.Error()})
		return
	}
	status := http.StatusOK
	if out.Changed {
		// what would apply changed since it was shown: nothing was applied
		status = http.StatusConflict
	} else {
		o.log().Info("switchover: the old home's kept-aside changes applied", "report", id, "by", p.UserID,
			"event", "kept_aside_applied")
	}
	common.WriteJSON(w, r, status, out)
}

func (o *Orchestrator) postDismiss(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	id, ok := reportID(w, r)
	if !ok || !o.supervised(w, r) {
		return
	}
	if err := o.Supervisor.Call(r.Context(), "kept-dismiss", map[string]any{"report": id, "by": "user:" + p.UserID},
		nil); err != nil {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/kept-aside", Title: "not set aside",
			Status: http.StatusConflict, Detail: err.Error()})
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"dismissed": true})
}

func (o *Orchestrator) supervised(w http.ResponseWriter, r *http.Request) bool {
	if o.Supervisor == nil {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/switchover-unavailable",
			Title: "not available here", Status: http.StatusConflict,
			Detail: "this home does not run under Maktaba's supervisor with its own Postgres"})
		return false
	}
	return true
}
