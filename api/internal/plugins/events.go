package plugins

// Published events (README §4.12, story 54.40 §1, §2): events/emit. A plugin emits only an
// event its calling stage declares in `emits`, about a subject in the job's scope, with a
// JSON object payload the event's schema accepts. The answer names the subscribing stages
// it goes to: every enabled plugin with a trigger on plugin:<this id>/<event> whose grants
// hold events.subscribe:<this id>. A subscriber the lineage would loop through refuses the
// whole emit (plugin_loop). The host hands the event to the pipeline in the job's result,
// which enqueues it to those stages only when the job ended well.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// MaxEvent is the largest event payload (README §4.7).
const MaxEvent = 16 << 10

// emitManifest is what the API reads of a manifest for events.
type emitManifest struct {
	Stage []struct {
		Name    string   `json:"name"`
		Subject string   `json:"subject"`
		Emits   []string `json:"emits"`
		Trigger []struct {
			Event string `json:"event"`
		} `json:"trigger"`
	} `json:"stage"`
	Event []struct {
		Name    string `json:"name"`
		Subject string `json:"subject"`
		Payload string `json:"payload"`
	} `json:"event"`
}

// subscribers answers the queue names of the stages that take event plugin:<id>/<name>:
// enabled plugins whose grants hold events.subscribe:<id>.
func (s *Server) subscribers(ctx context.Context, publisher, name string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT i.plugin_id, i.manifest, COALESCE(g.capabilities, '{}')
		FROM plugin_installs i JOIN components c ON c.id = $1 || i.plugin_id AND c.want = $2 AND c.hold_by IS NULL
		LEFT JOIN plugin_grants g ON g.plugin_id = i.plugin_id
		WHERE i.plugin_id <> $3 ORDER BY i.plugin_id`, components.PluginPrefix, components.WantEnabled, publisher)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	want := "plugin:" + publisher + "/" + name
	var out []string
	for rows.Next() {
		var id string
		var raw []byte
		var caps pq.StringArray
		if err := rows.Scan(&id, &raw, &caps); err != nil {
			return nil, err
		}
		if !slices.Contains(caps, "events.subscribe:"+publisher) {
			continue
		}
		var m emitManifest
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		for _, st := range m.Stage {
			for _, t := range st.Trigger {
				if t.Event == want {
					out = append(out, QueueName(id, st.Name))
				}
			}
		}
	}
	sort.Strings(out)
	return out, rows.Err()
}

func (s *Server) eventRoutes() {
	s.routes["events/emit"] = func(c *call) (any, *Refusal) {
		var in struct {
			Name    string     `json:"name"`
			Subject subjectRef `json:"subject"`
			Payload string     `json:"payload"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if len(in.Payload) > MaxEvent || !utf8.ValidString(in.Payload) {
			return nil, refuse(413, CodeTooLarge, "an event payload is at most 16 KB")
		}
		var raw []byte
		var path string
		if err := c.s.DB.QueryRowContext(c.ctx, `SELECT manifest, path FROM plugin_installs WHERE plugin_id = $1`,
			c.inv.PluginID).Scan(&raw, &path); err != nil {
			return nil, dbErr(err)
		}
		var m emitManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, dbErr(err)
		}
		declared := false
		for _, st := range m.Stage {
			if QueueName(c.inv.PluginID, st.Name) == c.inv.Stage && slices.Contains(st.Emits, in.Name) {
				declared = true
			}
		}
		if !declared {
			return nil, refuse(403, CodeScope, "only an event this stage emits")
		}
		var schemaPath, subject string
		for _, e := range m.Event {
			if e.Name == in.Name {
				schemaPath, subject = e.Payload, e.Subject
			}
		}
		if in.Subject.Kind != subject {
			return nil, refuse(403, CodeScope, "the event is about a "+subject)
		}
		if in.Subject.Kind == "video" {
			if ref := c.readScope(in.Subject.ID); ref != nil {
				return nil, ref
			}
		}
		if strings.Contains(in.Payload, `"`+LineageKey+`"`) {
			return nil, refuse(400, CodeInvalid, LineageKey+" is Maktaba's")
		}
		if ref := checkPayload(filepath.Join(path, filepath.FromSlash(schemaPath)), in.Payload); ref != nil {
			return nil, ref
		}
		c.inv.mu.Lock()
		c.inv.requests++
		over := c.inv.requests > MaxRequests
		c.inv.mu.Unlock()
		if over {
			return nil, refuse(403, CodeQuota, "at most 1,000 requests and events a job")
		}
		stages, err := c.s.subscribers(c.ctx, c.inv.PluginID, in.Name)
		if err != nil {
			return nil, dbErr(err)
		}
		key := subjectKey(in.Subject.Kind, in.Subject.ID)
		for _, st := range stages {
			if _, ok := c.inv.Lineage.Next(st, key); !ok {
				return nil, refuse(403, CodeLoop, fmt.Sprintf("%s on %s would loop", st, key))
			}
		}
		if stages == nil {
			stages = []string{}
		}
		return map[string]any{"stages": stages}, nil
	}
}

// checkPayload checks an event's payload against its declared schema: a JSON object; the
// schema's properties (type, enum, minimum, maximum, maxLength, items), required and
// additionalProperties. The schema file is the publisher's, in its package.
func checkPayload(schemaFile, payload string) *Refusal {
	if payload == "" {
		payload = "{}"
	}
	var v map[string]any
	if json.Unmarshal([]byte(payload), &v) != nil {
		return refuse(400, CodeInvalid, "an event payload is a JSON object")
	}
	raw, err := os.ReadFile(schemaFile)
	if err != nil {
		return refuse(400, CodeInvalid, "the event has no payload schema")
	}
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil {
		return refuse(400, CodeInvalid, "the event's payload schema is not JSON")
	}
	if why := matchSchema(schema, v, ""); why != "" {
		return refuse(400, CodeInvalid, "the payload does not match its schema: "+why)
	}
	return nil
}

func matchSchema(s map[string]any, v any, at string) string {
	if at == "" {
		at = "payload"
	}
	switch t, _ := s["type"].(string); t {
	case "object":
		o, ok := v.(map[string]any)
		if !ok {
			return at + " is an object"
		}
		props, _ := s["properties"].(map[string]any)
		for _, r := range anyList(s["required"]) {
			if _, ok := o[fmt.Sprint(r)]; !ok {
				return at + "." + fmt.Sprint(r) + " is required"
			}
		}
		for k, x := range o {
			p, ok := props[k].(map[string]any)
			if !ok {
				if s["additionalProperties"] == false {
					return at + "." + k + " is not in the schema"
				}
				continue
			}
			if why := matchSchema(p, x, at+"."+k); why != "" {
				return why
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return at + " is an array"
		}
		if n, ok := s["maxItems"].(float64); ok && float64(len(a)) > n {
			return at + " has too many items"
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, x := range a {
				if why := matchSchema(items, x, fmt.Sprintf("%s[%d]", at, i)); why != "" {
					return why
				}
			}
		}
	case "string":
		str, ok := v.(string)
		if !ok {
			return at + " is a string"
		}
		if n, ok := s["maxLength"].(float64); ok && float64(utf8.RuneCountInString(str)) > n {
			return at + " is too long"
		}
	case "integer", "number":
		f, ok := v.(float64)
		if !ok || (t == "integer" && f != float64(int64(f))) {
			return at + " is a " + t
		}
		if n, ok := s["minimum"].(float64); ok && f < n {
			return at + " is under its minimum"
		}
		if n, ok := s["maximum"].(float64); ok && f > n {
			return at + " is over its maximum"
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return at + " is a boolean"
		}
	}
	if enum := anyList(s["enum"]); len(enum) > 0 && !slices.ContainsFunc(enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(v) }) {
		return at + " is not one of the allowed values"
	}
	return ""
}

func anyList(v any) []any { l, _ := v.([]any); return l }
