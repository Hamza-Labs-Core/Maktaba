package plugins

// Plugins on joined machines (README §4.13, story 54.42): a machine serves the plugin API
// for its own jobs on its own unix socket: the token check, media handles and every read
// it may make through its database role. The plugin tables refuse a machine's writes
// (plugin_tables_guard, slot 0194), so every call that writes, and every call that spends
// a quota or extends a lineage, goes to the home's plugin API over the machine's signed
// link, and the home applies it under its own role with the same checks. Tokens are
// minted by the home (it writes plugin_invocations) and adopted by the machine. So the
// guard stays, and one place enforces quotas and lineage.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// CodeHomeUnreachable is a forwarded call the home did not answer.
const CodeHomeUnreachable = "plugin_home_unreachable"

// Forwarded are the plugin API routes a machine sends to its home: each writes a plugin
// table or core data, or spends a quota or a lineage the home keeps.
var Forwarded = []string{
	"data/put", "data/delete",
	"write/set-field", "write/add-tags", "write/set-chapters", "write/add-subtitle", "write/set-artwork",
	"suggest/add", "notify/admin", "queue/request", "events/emit",
}

// IsForwarded reports whether a machine sends route to the home.
func IsForwarded(route string) bool { return slices.Contains(Forwarded, route) }

// WireInvocation is an invocation as the home hands it to the machine that runs the job.
type WireInvocation struct {
	ID              string              `json:"id"`
	Token           string              `json:"token"`
	PluginID        string              `json:"plugin_id"`
	Version         string              `json:"version"`
	JobID           int64               `json:"job_id"`
	Stage           string              `json:"stage"`
	SubjectKind     string              `json:"subject_kind"`
	SubjectID       string              `json:"subject_id"`
	Libraries       []string            `json:"libraries"`
	AllLibraries    bool                `json:"all_libraries"`
	Grants          []string            `json:"grants"`
	GrantGeneration int64               `json:"grant_generation"`
	Exp             time.Time           `json:"exp"`
	DataBytes       int64               `json:"data_bytes"`
	MayRequest      map[string][]string `json:"may_request"`
	Lineage         Lineage             `json:"lineage"`
}

// Remote is the home as a machine's tokens and plugin API reach it.
type Remote interface {
	Mint(ctx context.Context, jobID int64) (*WireInvocation, error)
	End(ctx context.Context, id string) (int64, error)
	// Call sends one plugin API call with the job's token; it answers the home's status,
	// content type and body.
	Call(ctx context.Context, route, token string, body []byte) (int, string, []byte, error)
	// Outcome records a job's outcome in the plugin's breaker on the home (54.09 §2).
	Outcome(ctx context.Context, o Outcome) (string, error)
}

// HTTPRemote is a Remote over the machine's signed link (homelink.Link's Transport).
type HTTPRemote struct {
	Home      string
	Transport http.RoundTripper
}

func (h *HTTPRemote) do(ctx context.Context, method, path, token string, body []byte) (int, string, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.Home, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Plugin "+token)
	}
	rt := h.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	resp, err := (&http.Client{Transport: rt, Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 9<<20))
	return resp.StatusCode, resp.Header.Get("Content-Type"), out, err
}

func (h *HTTPRemote) Mint(ctx context.Context, jobID int64) (*WireInvocation, error) {
	b, _ := json.Marshal(map[string]int64{"job_id": jobID})
	status, _, out, err := h.do(ctx, http.MethodPost, ClusterMintPath, "", b)
	if err != nil {
		return nil, refuse(503, CodeHomeUnreachable, err.Error())
	}
	if status != http.StatusOK {
		var r struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(out, &r)
		if r.Code == "" {
			r.Code = CodeHomeUnreachable
		}
		return nil, refuse(status, r.Code, "the home refused the token")
	}
	var w WireInvocation
	if err := json.Unmarshal(out, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

func (h *HTTPRemote) End(ctx context.Context, id string) (int64, error) {
	status, _, out, err := h.do(ctx, http.MethodDelete, ClusterMintPath+"/"+id, "", nil)
	if err != nil || status != http.StatusOK {
		return 0, fmt.Errorf("plugins: the home did not end %s: %d %v", id, status, err)
	}
	var r struct {
		Refusals int64 `json:"refusals"`
	}
	_ = json.Unmarshal(out, &r)
	return r.Refusals, nil
}

func (h *HTTPRemote) Call(ctx context.Context, route, token string, body []byte) (int, string, []byte, error) {
	return h.do(ctx, http.MethodPost, ClusterAPIPrefix+route, token, body)
}

func (h *HTTPRemote) Outcome(ctx context.Context, o Outcome) (string, error) {
	b, _ := json.Marshal(o)
	status, _, out, err := h.do(ctx, http.MethodPost, ClusterOutcomePath, "", b)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("plugins: the home did not record the outcome: %d %v", status, err)
	}
	var r struct {
		Tripped string `json:"tripped"`
	}
	_ = json.Unmarshal(out, &r)
	return r.Tripped, nil
}

// The home's routes for its machines (served only to a request a machine signed).
const (
	ClusterMintPath    = "/internal/cluster/plugins/invocations"
	ClusterAPIPrefix   = "/internal/cluster/plugin-api/v1/"
	ClusterOutcomePath = "/internal/cluster/plugins/outcome"
	ClusterFilesPrefix = "/internal/cluster/plugins/files/"
)

// MintFor mints a token on the home for a job a joined machine runs: the plugin must be
// enabled, not held, and placed where a machine may run it (any or gpu); the answer is the
// whole invocation, which the machine adopts.
func (t *Tokens) MintFor(ctx context.Context, jobID int64) (*WireInvocation, error) {
	var ext, want string
	var held bool
	var raw []byte
	err := t.DB.QueryRowContext(ctx, `SELECT COALESCE(s.extension, ''), COALESCE(c.want, ''), c.hold_by IS NOT NULL,
		COALESCE(i.manifest, '{}')
		FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
		LEFT JOIN plugin_installs i ON 'plugin:' || i.plugin_id = s.extension
		LEFT JOIN components c ON c.id = $2 || i.plugin_id
		WHERE j.id = $1`, jobID, components.PluginPrefix).Scan(&ext, &want, &held, &raw)
	if err != nil {
		return nil, refuse(404, CodeNotFound, "no such job")
	}
	var m struct {
		Placement string `json:"placement"`
	}
	_ = json.Unmarshal(raw, &m)
	switch {
	case !strings.HasPrefix(ext, "plugin:"):
		return nil, refuse(409, CodeNotPluginJob, "the job's stage is not a plugin's")
	case want != components.WantEnabled || held:
		return nil, refuse(409, CodePluginOff, "the plugin is off or held")
	case components.RunsOnMachine(m.Placement, true) != "":
		return nil, refuse(403, CodeScope, "the plugin runs on the home only")
	}
	mt, err := t.Mint(ctx, jobID)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	inv := t.byID[mt.InvocationID]
	t.mu.Unlock()
	if inv == nil {
		return nil, errors.New("plugins: the minted invocation is gone")
	}
	return &WireInvocation{ID: inv.ID, Token: mt.Token, PluginID: inv.PluginID, Version: inv.Version, JobID: inv.JobID,
		Stage: inv.Stage, SubjectKind: inv.SubjectKind, SubjectID: inv.SubjectID, Libraries: nonNil(inv.Libraries),
		AllLibraries: inv.AllLibraries, Grants: nonNil(inv.Grants), GrantGeneration: inv.GrantGeneration, Exp: inv.Exp,
		DataBytes: inv.DataBytes, MayRequest: inv.MayRequest, Lineage: inv.Lineage}, nil
}

// CodePluginOff: a machine asked for a token of a plugin that is off or held.
const CodePluginOff = "plugin_off"

// adopt registers an invocation the home minted (a machine's tokens).
func (t *Tokens) adopt(w *WireInvocation) *Minted {
	now := t.Now()
	inv := &Invocation{ID: w.ID, PluginID: w.PluginID, Version: w.Version, JobID: w.JobID, Stage: w.Stage,
		SubjectKind: w.SubjectKind, SubjectID: w.SubjectID, Libraries: w.Libraries, AllLibraries: w.AllLibraries,
		Grants: w.Grants, GrantGeneration: w.GrantGeneration, Exp: w.Exp, DataBytes: w.DataBytes,
		MayRequest: w.MayRequest, Lineage: w.Lineage, bucket: RateBurst, bucketAt: now, handles: map[uint64]*handle{}}
	if inv.MayRequest == nil {
		inv.MayRequest = map[string][]string{}
	}
	t.mu.Lock()
	t.byHash[hashToken(w.Token)], t.byID[inv.ID] = inv, inv
	t.mu.Unlock()
	return &Minted{InvocationID: inv.ID, Token: w.Token, PluginID: inv.PluginID, Version: inv.Version,
		Grants: nonNil(inv.Grants), Libraries: nonNil(inv.Libraries), AllLibraries: inv.AllLibraries, Exp: inv.Exp,
		Lineage: inv.Lineage}
}

// enabledHere checks, on a machine, that the plugin is still on at the home (a disable,
// a hold or a revocation reaches a machine's next read at once).
func (t *Tokens) enabledHere(ctx context.Context, pluginID string) *Refusal {
	var want string
	var held bool
	err := t.DB.QueryRowContext(ctx, `SELECT COALESCE(want, ''), hold_by IS NOT NULL FROM components WHERE id = $1`,
		components.PluginPrefix+pluginID).Scan(&want, &held)
	if err != nil || want != components.WantEnabled || held {
		return refuse(401, CodeToken, "the plugin is off")
	}
	return nil
}

func slogWarn(msg, id string, err error) {
	slog.Default().Warn(msg, "invocation", id, "err", err)
}
