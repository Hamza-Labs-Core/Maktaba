package plugins

// The flow check on the home (README §4.12, story 54.39 §6): at install, update, enable
// and rollback the combined graph of core events and every enabled plugin, with the
// change, must keep the leaf rule and have no cycle. The check is shared/plugins/
// pluginflow, the one the store runs at verification (54.16 C12) in TypeScript.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginflow"
)

// Codes of the flow check (words: plugins.error.<code>, and each finding's own words).
const (
	CodeFlowCycle   = "plugin_flow_cycle"
	CodeFlowInvalid = "plugin_flow_invalid"
)

// FlowFinding is a finding with its words in English and Arabic.
type FlowFinding struct {
	pluginflow.Finding
	Message   string `json:"message"`
	MessageAr string `json:"message_ar"`
}

func withWords(fs []pluginflow.Finding) []FlowFinding {
	out := make([]FlowFinding, 0, len(fs))
	for _, f := range fs {
		out = append(out, FlowFinding{Finding: f, Message: pluginflow.Message(f, "en"), MessageAr: pluginflow.Message(f, "ar")})
	}
	return out
}

// homeMembers reads every installed plugin as a member of the home's graph, its state
// from the desired row (enabled, or installed when off or held off).
func (l *Lifecycle) homeMembers(ctx context.Context) ([]pluginflow.Member, error) {
	rows, err := l.DB.QueryContext(ctx, `SELECT i.plugin_id, i.manifest, COALESCE(c.want, '')
		FROM plugin_installs i LEFT JOIN components c ON c.id = $1 || i.plugin_id ORDER BY i.plugin_id`, components.PluginPrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pluginflow.Member
	for rows.Next() {
		var id, want string
		var raw []byte
		if err := rows.Scan(&id, &raw, &want); err != nil {
			return nil, err
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		state := pluginflow.StateInstalled
		if want == components.WantEnabled {
			state = pluginflow.StateEnabled
		}
		out = append(out, pluginflow.FromManifest(m, state))
	}
	return out, rows.Err()
}

// checkFlow runs the check with plugin id's manifest (raw, normalised) enabled; folder is
// its package folder, whose flow.toml (when it has one) the manifest must match. A
// refusal is a 409 naming each finding in English and Arabic; warnings pass.
func (l *Lifecycle) checkFlow(ctx context.Context, id string, raw []byte, folder string) error {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	var findings []pluginflow.Finding
	if folder != "" {
		src, err := os.ReadFile(filepath.Join(folder, "flow.toml"))
		switch {
		case err == nil:
			f, fs := pluginflow.ParseFlow(src)
			findings = append(findings, fs...)
			if fs == nil {
				findings = append(findings, pluginflow.MatchManifest(f, m)...)
			}
		case !errors.Is(err, os.ErrNotExist):
			return err
		}
	}
	others, err := l.homeMembers(ctx)
	if err != nil {
		return err
	}
	members := []pluginflow.Member{pluginflow.FromManifest(m, pluginflow.StateEnabled)}
	for _, o := range others {
		if o.ID != id {
			members = append(members, o)
		}
	}
	_, fs := pluginflow.Check(members, pluginflow.Options{})
	findings = append(findings, fs...)
	if !pluginflow.HasRefusal(findings) {
		return nil
	}
	code := CodeFlowInvalid
	var first *pluginflow.Finding
	for i, f := range findings {
		if f.Severity != pluginflow.Refuse {
			continue
		}
		if first == nil {
			first = &findings[i]
		}
		if f.Code == pluginflow.CodeCycle {
			code, first = CodeFlowCycle, &findings[i]
			break
		}
	}
	p := problem(http.StatusConflict, code, "%s", pluginflow.Message(*first, "en"))
	p.Params = map[string]any{"findings": withWords(findings), "path": first.Path,
		"message": pluginflow.Message(*first, "en"), "message_ar": pluginflow.Message(*first, "ar")}
	return p
}

// HomeGraph is GET /api/plugins/graph: the whole home's plugin graph and its findings.
type HomeGraph struct {
	Graph    pluginflow.Graph `json:"graph"`
	Findings []FlowFinding    `json:"findings"`
}

// Graph answers the home's plugin graph for the chart (54.41).
func (l *Lifecycle) Graph(ctx context.Context) (*HomeGraph, error) {
	members, err := l.homeMembers(ctx)
	if err != nil {
		return nil, err
	}
	g, fs := pluginflow.HomeGraph(members)
	return &HomeGraph{Graph: g, Findings: withWords(fs)}, nil
}

// Downstream answers the other plugins' stages that go idle when plugin id is off.
func (l *Lifecycle) Downstream(ctx context.Context, id string) ([]string, error) {
	members, err := l.homeMembers(ctx)
	if err != nil {
		return nil, err
	}
	g, _ := pluginflow.HomeGraph(members)
	out := pluginflow.Downstream(g, id)
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// FlowView is GET /api/plugins/{id}/flow: the plugin's own flow (54.41 §2), the whole set
// it works with, each member's state on this home, and the members not installed.
type FlowView struct {
	ID       string           `json:"id"`
	Version  string           `json:"version"`
	Name     string           `json:"name"`
	Graph    pluginflow.Graph `json:"graph"`
	Missing  []FlowMember     `json:"missing"`
	Findings []FlowFinding    `json:"findings"`
}

// FlowMember names a member of a flow and the versions the flow takes.
type FlowMember struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Flow answers plugin id's flow as this home has it: members installed here as their
// manifests say, the others as the flow declares them, marked missing. A plugin with no
// flow.toml is the flow of itself.
func (l *Lifecycle) Flow(ctx context.Context, id string) (*FlowView, error) {
	in, err := loadInstall(ctx, l.DB, id, false)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, problem(http.StatusNotFound, CodeNotInstalled, "%s is not installed", id)
	}
	home, err := l.homeMembers(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]pluginflow.Member{}
	for _, m := range home {
		byID[m.ID] = m
	}
	view := &FlowView{ID: id, Version: in.Version, Missing: []FlowMember{}}
	var members []pluginflow.Member
	src, err := os.ReadFile(filepath.Join(in.Path, "flow.toml"))
	switch {
	case err == nil:
		f, fs := pluginflow.ParseFlow(src)
		if fs != nil {
			view.Findings = withWords(fs)
			break
		}
		view.ID, view.Version, view.Name = f.ID, f.Version, f.Name
		for _, fm := range f.Members {
			if m, ok := byID[fm.ID]; ok {
				members = append(members, m)
				continue
			}
			fm.State = pluginflow.StateMissing
			members = append(members, fm)
			view.Missing = append(view.Missing, FlowMember{ID: fm.ID, Version: fm.Version})
		}
	case errors.Is(err, os.ErrNotExist):
		members = append(members, byID[id])
	default:
		return nil, err
	}
	g, fs := pluginflow.HomeGraph(members)
	view.Graph = g
	if view.Findings == nil {
		view.Findings = withWords(fs)
	}
	return view, nil
}
