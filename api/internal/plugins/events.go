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
	"errors"
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

// The reasons a refused emit is logged under (plugin_emit_refused), next to the refusal's
// code: the same shape as the worker's plugin_publish_refused (#1021).
const (
	EmitDecode          = "decode"           // the request, or its payload, is not JSON of its shape
	EmitOversize        = "oversize"         // a payload over 16 KB, or a request over 6 MB
	EmitEncoding        = "encoding"         // a payload that is not UTF-8
	EmitUnknownKind     = "unknown_kind"     // an event the calling stage does not declare in emits
	EmitScope           = "scope"            // a subject of another kind, or out of the job's scope
	EmitLineage         = "lineage"          // a payload naming Maktaba's _lineage
	EmitSchema          = "schema"           // a payload its event's schema refuses, or no schema
	EmitQuota           = "quota"            // over the job's 1,000 requests and events
	EmitLoop            = "loop"             // a subscriber the lineage would loop through
	EmitToken           = "token"            // no token, an unknown or ended one, or one this plugin may not use now
	EmitRate            = "rate"             // over 200 calls a second
	EmitUnavailable     = "unavailable"      // the database did not answer
	EmitHomeUnreachable = "home_unreachable" // a joined machine could not forward it to its home
)

// emitRoute is the route events/emit names.
const emitRoute = "events/emit"

// emitShape is what a log line reads of an emit request: never the payload itself.
type emitShape struct {
	kind string
	size int // the payload's bytes; the request's when it does not decode
}

// peekEmit reads the kind and the payload's size from an emit request's body, as far as it
// decodes; a body that does not decode has no kind and its own size.
func peekEmit(body []byte) emitShape {
	var in struct {
		Name    json.RawMessage `json:"name"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(body, &in) != nil {
		return emitShape{size: len(body)}
	}
	var sh emitShape
	_ = json.Unmarshal(in.Name, &sh.kind)
	var payload string
	if json.Unmarshal(in.Payload, &payload) == nil {
		sh.size = len(payload)
	} else {
		sh.size = len(in.Payload)
	}
	return sh
}

// logEmitRefused logs one refused emit (plugin_emit_refused) with the plugin ("" when the
// token names none), the kind, the payload's size, the reason and a detail that never
// carries the payload. The answer to the plugin is the refusal, unchanged.
func (s *Server) logEmitRefused(inv *Invocation, plugin string, sh emitShape, reason string, ref *Refusal) {
	attrs := []any{"plugin", plugin, "kind", clip(sh.kind, 100), "size", sh.size, "reason", reason,
		"code", ref.Code, "detail", ref.Detail}
	if inv != nil {
		attrs = append(attrs, "job_id", inv.JobID)
	}
	s.logger().Warn("plugin_emit_refused", attrs...)
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// decodeDetail says why a request did not decode without quoting it.
func decodeDetail(err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		return fmt.Sprintf("the request is not JSON (at byte %d)", syn.Offset)
	case errors.As(err, &typ):
		return fmt.Sprintf("the request's %s is not a %s", typ.Field, typ.Type)
	}
	return "the request is not JSON of events/emit's shape"
}

func (s *Server) eventRoutes() {
	s.routes[emitRoute] = func(c *call) (any, *Refusal) {
		out, reason, sh, ref := c.emit()
		if ref != nil {
			c.s.logEmitRefused(c.inv, c.inv.PluginID, sh, reason, ref)
		}
		return out, ref
	}
}

// emit is events/emit: its answer, or the refusal with the reason it is logged under and
// the kind and size it is logged with.
func (c *call) emit() (any, string, emitShape, *Refusal) {
	var in struct {
		Name    string     `json:"name"`
		Subject subjectRef `json:"subject"`
		Payload string     `json:"payload"`
	}
	if err := json.Unmarshal(c.body, &in); err != nil {
		// the answer is the decode error, as every route's; the log never quotes the body
		return nil, EmitDecode, peekEmit(c.body), refuse(400, CodeInvalid, decodeDetail(err))
	}
	sh := emitShape{kind: in.Name, size: len(in.Payload)}
	no := func(reason string, ref *Refusal) (any, string, emitShape, *Refusal) { return nil, reason, sh, ref }
	if len(in.Payload) > MaxEvent {
		return no(EmitOversize, refuse(413, CodeTooLarge, "an event payload is at most 16 KB"))
	}
	if !utf8.ValidString(in.Payload) {
		return no(EmitEncoding, refuse(413, CodeTooLarge, "an event payload is at most 16 KB"))
	}
	var raw []byte
	var path string
	if err := c.s.DB.QueryRowContext(c.ctx, `SELECT manifest, path FROM plugin_installs WHERE plugin_id = $1`,
		c.inv.PluginID).Scan(&raw, &path); err != nil {
		return no(EmitUnavailable, dbErr(err))
	}
	var m emitManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return no(EmitUnavailable, dbErr(err))
	}
	declared := false
	for _, st := range m.Stage {
		if QueueName(c.inv.PluginID, st.Name) == c.inv.Stage && slices.Contains(st.Emits, in.Name) {
			declared = true
		}
	}
	if !declared {
		return no(EmitUnknownKind, refuse(403, CodeScope, "only an event this stage emits"))
	}
	var schemaPath, subject string
	for _, e := range m.Event {
		if e.Name == in.Name {
			schemaPath, subject = e.Payload, e.Subject
		}
	}
	if in.Subject.Kind != subject {
		return no(EmitScope, refuse(403, CodeScope, "the event is about a "+subject))
	}
	if _, ref := c.subjectScope(in.Subject, false); ref != nil {
		return no(EmitScope, ref)
	}
	if strings.Contains(in.Payload, `"`+LineageKey+`"`) {
		return no(EmitLineage, refuse(400, CodeInvalid, LineageKey+" is Maktaba's"))
	}
	if reason, ref := checkPayload(filepath.Join(path, filepath.FromSlash(schemaPath)), in.Payload); ref != nil {
		return no(reason, ref)
	}
	c.inv.mu.Lock()
	c.inv.requests++
	over := c.inv.requests > MaxRequests
	c.inv.mu.Unlock()
	if over {
		return no(EmitQuota, refuse(403, CodeQuota, "at most 1,000 requests and events a job"))
	}
	stages, err := c.s.subscribers(c.ctx, c.inv.PluginID, in.Name)
	if err != nil {
		return no(EmitUnavailable, dbErr(err))
	}
	key := subjectKey(in.Subject.Kind, in.Subject.ID)
	for _, st := range stages {
		if _, ok := c.inv.Lineage.Next(st, key); !ok {
			return no(EmitLoop, refuse(403, CodeLoop, fmt.Sprintf("%s on %s would loop", st, key)))
		}
	}
	if stages == nil {
		stages = []string{}
	}
	return map[string]any{"stages": stages}, "", sh, nil
}

// checkPayload checks an event's payload against its declared schema: a JSON object; the
// schema's properties (type, enum, minimum, maximum, maxLength, items), required and
// additionalProperties. The schema file is the publisher's, in its package. A refusal comes
// with the reason it is logged under (EmitDecode or EmitSchema); its detail names schema
// paths only, never a value or a key of the payload's own.
func checkPayload(schemaFile, payload string) (string, *Refusal) {
	if payload == "" {
		payload = "{}"
	}
	var v map[string]any
	if json.Unmarshal([]byte(payload), &v) != nil {
		return EmitDecode, refuse(400, CodeInvalid, "an event payload is a JSON object")
	}
	raw, err := os.ReadFile(schemaFile)
	if err != nil {
		return EmitSchema, refuse(400, CodeInvalid, "the event has no payload schema")
	}
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil {
		return EmitSchema, refuse(400, CodeInvalid, "the event's payload schema is not JSON")
	}
	if why := matchSchema(schema, v, ""); why != "" {
		return EmitSchema, refuse(400, CodeInvalid, "the payload does not match its schema: "+why)
	}
	return "", nil
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
					return at + " has a property the schema does not name" // never the payload's own key
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
