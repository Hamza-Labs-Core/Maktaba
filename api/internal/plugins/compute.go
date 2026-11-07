package plugins

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
)

// Local compute (story 54.21): compute.embed asks the pipeline's embedder (the one semantic
// search uses), compute.llm the home's language model. The plugin host has no network: the
// calls come here on the unix socket, and this is Maktaba's code asking Maktaba's models.
// A model server that is not on the home or the LAN is refused (compute_remote), checked on
// every connection, not only on the name.

// Compute refusal codes (the host maps compute_unavailable to call-error.unavailable; the
// others reach the plugin as invalid(<code>)).
const (
	CodeComputeUnavailable = "compute_unavailable"
	CodeComputeRemote      = "compute_remote"
	CodeComputeBadOutput   = "compute_bad_output"
)

// Limits of one call (54.21 §1).
const (
	MaxEmbedTexts = 64
	MaxEmbedChars = 2000
	MaxPrompt     = 32 << 10
	MaxTokens     = 1024
)

// ComputeRules is rules.plugin_compute (pipeline plugins/compute_settings.py, the schema's
// source); the defaults are kept equal to schema.json by a test.
type ComputeRules struct {
	URL              string
	Model            string
	GeneratePerDay   int
	EmbedTextsPerDay int
	TimeoutS         int
}

// DefaultComputeRules are the built-in rules.
func DefaultComputeRules() ComputeRules {
	return ComputeRules{GeneratePerDay: 2000, EmbedTextsPerDay: 50000, TimeoutS: 120}
}

// Embedder embeds texts as passages with the pipeline's model; it answers the model id.
type Embedder interface {
	EmbedTexts(ctx context.Context, texts []string) (model string, vectors [][]float32, err error)
}

// ErrComputeOff is what an Embedder answers while search.semantic is off.
var ErrComputeOff = errors.New("compute: the component is off")

// Compute holds what the compute routes ask.
type Compute struct {
	// Embedder is the pipeline's (nil: compute.embed is unavailable).
	Embedder Embedder
	// LLMOn answers whether the llm component is on (nil: on).
	LLMOn func(ctx context.Context) bool
	// Resolve looks a model server's name up (nil: the system resolver).
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	// Now is the clock (nil: time.Now).
	Now func() time.Time
	// LeaseWait is how often a question waiting for a lookup slot looks again.
	LeaseWait time.Duration
	// Env is the environment (nil: os.Getenv), for MAKTABA_OLLAMA_URL.
	Env func(string) string
}

func (k *Compute) now() time.Time {
	if k != nil && k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

// rules reads rules.plugin_compute, with the tag check's server and model where it names
// none (the classify model is the default, 54.21 §1).
func (k *Compute) rules(ctx context.Context, db *sql.DB) (ComputeRules, string, string) {
	cfg := DefaultComputeRules()
	if m, err := rules.Effective(ctx, db, "plugin_compute"); err == nil {
		cfg.URL, _ = m["url"].(string)
		cfg.Model, _ = m["model"].(string)
		setInt(m["generate_per_day"], &cfg.GeneratePerDay)
		setInt(m["embed_texts_per_day"], &cfg.EmbedTextsPerDay)
		setInt(m["timeout_s"], &cfg.TimeoutS)
	}
	server, model := strings.TrimSpace(cfg.URL), strings.TrimSpace(cfg.Model)
	if server == "" || model == "" {
		tcURL, tcModel := "http://localhost:11434", "qwen2.5:7b"
		if m, err := rules.Effective(ctx, db, "tagcheck"); err == nil {
			if s, ok := m["url"].(string); ok && s != "" {
				tcURL = s
			}
			if s, ok := m["model"].(string); ok && s != "" {
				tcModel = s
			}
		}
		// the machine's own server (rules.ENV_DEFAULTS) when the tag check's is unchanged
		env := os.Getenv
		if k != nil && k.Env != nil {
			env = k.Env
		}
		if v := strings.TrimRight(strings.TrimSpace(env("MAKTABA_OLLAMA_URL")), "/"); v != "" && tcURL == "http://localhost:11434" {
			tcURL = v
		}
		if server == "" {
			server = tcURL
		}
		if model == "" {
			model = tcModel
		}
	}
	return cfg, strings.TrimRight(server, "/"), model
}

func setInt(v any, dst *int) {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil && i >= 0 {
			*dst = int(i)
		}
	case float64:
		if n >= 0 {
			*dst = int(n)
		}
	case int:
		if n >= 0 {
			*dst = n
		}
	}
}

// LocalAddr is whether a model server's address counts as local (54.21 §1): this machine
// (loopback) or the local network (RFC 1918, IPv6 unique-local, link-local). Everything
// else, the internet included, is remote.
func LocalAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast()
}

func (k *Compute) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{a}, nil
	}
	if k != nil && k.Resolve != nil {
		return k.Resolve(ctx, host)
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// localClient answers an HTTP client whose every connection goes to a local address only:
// the name is resolved here and each address checked before the dial, so a name that
// later points at the internet is refused too.
func (k *Compute) localClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			addrs, err := k.resolve(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, a := range addrs {
				if !LocalAddr(a) {
					return nil, errRemote
				}
			}
			if len(addrs) == 0 {
				return nil, errRemote
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(addrs[0].String(), port))
		},
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}

var errRemote = errors.New(CodeComputeRemote)

// CheckLocal answers compute_remote for a model server URL that is not on the home or the
// LAN (the name resolved now; every connection is checked again).
func (k *Compute) CheckLocal(ctx context.Context, server string) *Refusal {
	u, err := url.Parse(server)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return refuse(403, CodeComputeRemote, "the model server is not a URL on this home or the local network")
	}
	addrs, err := k.resolve(ctx, u.Hostname())
	if err != nil || len(addrs) == 0 {
		return refuse(503, CodeComputeUnavailable, "the model server's name does not resolve")
	}
	for _, a := range addrs {
		if !LocalAddr(a) {
			return refuse(403, CodeComputeRemote, "the model server is not on this home or the local network")
		}
	}
	return nil
}

// spend counts n units of kind ("generate_calls" or "embed_texts") against the plugin's
// daily limit, in one statement: over it, nothing is counted and plugin_quota answers.
func spend(ctx context.Context, db *sql.DB, pluginID string, day time.Time, kind string, n, limit int) *Refusal {
	if kind != "generate_calls" && kind != "embed_texts" {
		return refuse(500, CodeInvalid, "unknown compute kind")
	}
	if n > limit {
		return refuse(403, CodeQuota, fmt.Sprintf("over the plugin's %d a day", limit))
	}
	q := fmt.Sprintf(`INSERT INTO plugin_compute_usage (plugin_id, day, %[1]s) VALUES ($1, $2, $3)
		ON CONFLICT (plugin_id, day) DO UPDATE SET %[1]s = plugin_compute_usage.%[1]s + EXCLUDED.%[1]s
		WHERE plugin_compute_usage.%[1]s + EXCLUDED.%[1]s <= $4
		RETURNING %[1]s`, kind)
	var used int
	err := db.QueryRowContext(ctx, q, pluginID, day.UTC().Format("2006-01-02"), n, limit).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return refuse(403, CodeQuota, fmt.Sprintf("over the plugin's %d a day", limit))
	}
	if err != nil {
		return refuse(503, CodeComputeUnavailable, err.Error())
	}
	return nil
}

// LookupPool is the global pool plugins' questions share with core stages (54.21 §2).
const LookupPool = "lookup"

// takeLease waits for a lookup slot (the pool's claimers' lock, the same busy count as the
// claim with live leases, and the plugin share: while core work waits, a plugin takes a slot
// only after two core claims since its last one) and answers the lease id. It gives up when
// ctx ends.
func (k *Compute) takeLease(ctx context.Context, db *sql.DB, pluginID string, ttl time.Duration) (int64, *Refusal) {
	wait := 250 * time.Millisecond
	if k != nil && k.LeaseWait > 0 {
		wait = k.LeaseWait
	}
	for {
		id, ok, err := tryLease(ctx, db, pluginID, ttl)
		if err != nil {
			return 0, refuse(503, CodeComputeUnavailable, err.Error())
		}
		if ok {
			return id, nil
		}
		select {
		case <-ctx.Done():
			return 0, refuse(503, CodeComputeUnavailable, "no model slot came free in time")
		case <-time.After(wait):
		}
	}
}

// PluginShareOwner is the claim_shares row counting core claims of a pool since a plugin's
// last slot (the pipeline's claim adds one per claim, engine/claim.py).
func PluginShareOwner(pool string) string { return "plugin_share:" + pool }

func tryLease(ctx context.Context, db *sql.DB, pluginID string, ttl time.Duration) (int64, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "maktaba.pool:"+LookupPool); err != nil {
		return 0, false, err
	}
	var busy, size int
	var coreWaiting bool
	var since sql.NullFloat64
	err = tx.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM processing_jobs b JOIN job_stages bs ON bs.stage = b.stage
		  WHERE bs.pool = $1 AND b.state IN ('claimed', 'running', 'resuming'))
		+ (SELECT count(*) FROM compute_leases WHERE pool = $1 AND expires_at > now()),
		COALESCE((SELECT COALESCE(size, default_size, 1) FROM worker_pools WHERE pool = $1), 1),
		EXISTS (SELECT 1 FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
		         WHERE s.pool = $1 AND s.enabled AND NOT s.paused AND j.state = 'pending'
		           AND (j.not_before IS NULL OR j.not_before <= now())),
		(SELECT pass FROM claim_shares WHERE owner = $2)`, LookupPool, PluginShareOwner(LookupPool)).
		Scan(&busy, &size, &coreWaiting, &since)
	if err != nil {
		return 0, false, err
	}
	if busy >= size {
		return 0, false, nil
	}
	if coreWaiting && since.Valid && since.Float64 < 2 {
		return 0, false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM compute_leases WHERE pool = $1 AND expires_at <= now()`, LookupPool); err != nil {
		return 0, false, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO compute_leases (pool, plugin_id, expires_at)
		VALUES ($1, $2, now() + make_interval(secs => $3)) RETURNING id`, LookupPool, pluginID, ttl.Seconds()).Scan(&id); err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO claim_shares (owner, pass) VALUES ($1, 0)
		ON CONFLICT (owner) DO UPDATE SET pass = 0`, PluginShareOwner(LookupPool)); err != nil {
		return 0, false, err
	}
	return id, true, tx.Commit()
}

func endLease(db *sql.DB, id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = db.ExecContext(ctx, `DELETE FROM compute_leases WHERE id = $1`, id)
}

func (s *Server) computeRoutes() {
	s.routes["compute/embed"] = func(c *call) (any, *Refusal) {
		var in struct {
			Texts []string `json:"texts"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if len(in.Texts) == 0 {
			return nil, refuse(400, CodeInvalid, "at least one text")
		}
		if len(in.Texts) > MaxEmbedTexts {
			return nil, refuse(413, CodeTooLarge, "at most 64 texts")
		}
		for _, t := range in.Texts {
			if utf8.RuneCountInString(t) > MaxEmbedChars {
				return nil, refuse(413, CodeTooLarge, "a text is at most 2,000 characters")
			}
		}
		k := c.s.Compute
		if k == nil || k.Embedder == nil {
			return nil, refuse(503, CodeComputeUnavailable, "no embedder on this home")
		}
		cfg, _, _ := k.rules(c.ctx, c.s.DB)
		if ref := spend(c.ctx, c.s.DB, c.inv.PluginID, k.now(), "embed_texts", len(in.Texts), cfg.EmbedTextsPerDay); ref != nil {
			return nil, ref
		}
		start := k.now()
		model, vectors, err := k.Embedder.EmbedTexts(c.ctx, in.Texts)
		if err != nil {
			// sizes and timings only: never the texts (54.21 §2)
			c.s.logger().Warn("plugin_compute_failed", "plugin", c.inv.PluginID, "kind", "embed", "texts", len(in.Texts), "err", err)
			return nil, refuse(503, CodeComputeUnavailable, "the embedder is unavailable")
		}
		c.s.logger().Info("plugin_compute", "plugin", c.inv.PluginID, "kind", "embed", "texts", len(in.Texts),
			"ms", k.now().Sub(start).Milliseconds())
		return map[string]any{"model": model, "vectors": vectors}, nil
	}

	s.routes["compute/generate"] = func(c *call) (any, *Refusal) {
		var in struct {
			Prompt      string  `json:"prompt"`
			MaxTokens   int     `json:"max_tokens"`
			Temperature float64 `json:"temperature"`
			JSONSchema  *string `json:"json_schema"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if in.Prompt == "" {
			return nil, refuse(400, CodeInvalid, "a prompt")
		}
		if len(in.Prompt) > MaxPrompt {
			return nil, refuse(413, CodeTooLarge, "a prompt is at most 32 KB")
		}
		if in.MaxTokens < 1 || in.MaxTokens > MaxTokens {
			return nil, refuse(413, CodeTooLarge, "max-tokens is 1..1024")
		}
		if in.Temperature < 0 || in.Temperature > 2 {
			return nil, refuse(400, CodeInvalid, "temperature is 0..2")
		}
		var schema map[string]any
		if in.JSONSchema != nil && *in.JSONSchema != "" {
			if err := json.Unmarshal([]byte(*in.JSONSchema), &schema); err != nil {
				return nil, refuse(400, CodeInvalid, "json-schema is a JSON object")
			}
		}
		k := c.s.Compute
		if k == nil || (k.LLMOn != nil && !k.LLMOn(c.ctx)) {
			return nil, refuse(503, CodeComputeUnavailable, "the language model is off")
		}
		cfg, server, model := k.rules(c.ctx, c.s.DB)
		if ref := k.CheckLocal(c.ctx, server); ref != nil {
			return nil, ref
		}
		if ref := spend(c.ctx, c.s.DB, c.inv.PluginID, k.now(), "generate_calls", 1, cfg.GeneratePerDay); ref != nil {
			return nil, ref
		}
		timeout := time.Duration(cfg.TimeoutS) * time.Second
		ctx, cancel := context.WithTimeout(c.ctx, timeout)
		defer cancel()
		lease, ref := k.takeLease(ctx, c.s.DB, c.inv.PluginID, timeout+30*time.Second)
		if ref != nil {
			return nil, ref
		}
		defer endLease(c.s.DB, lease)
		start := k.now()
		client := k.localClient(timeout)
		var text string
		for attempt := 0; attempt < 2; attempt++ {
			out, err := ollamaGenerate(ctx, client, server, model, in.Prompt, in.MaxTokens, in.Temperature, schema)
			if err != nil {
				c.s.logger().Warn("plugin_compute_failed", "plugin", c.inv.PluginID, "kind", "generate",
					"prompt_bytes", len(in.Prompt), "remote", errors.Is(err, errRemote))
				if errors.Is(err, errRemote) {
					return nil, refuse(403, CodeComputeRemote, "the model server is not on this home or the local network")
				}
				return nil, refuse(503, CodeComputeUnavailable, "the language model is unavailable")
			}
			if schema == nil || followsSchema(out, schema) {
				text = out
				break
			}
			if attempt == 1 {
				return nil, refuse(422, CodeComputeBadOutput, "the answer did not follow json-schema")
			}
		}
		c.s.logger().Info("plugin_compute", "plugin", c.inv.PluginID, "kind", "generate",
			"prompt_bytes", len(in.Prompt), "answer_bytes", len(text), "ms", k.now().Sub(start).Milliseconds())
		return map[string]any{"text": text, "model": model}, nil
	}
}

// ollamaGenerate asks Ollama's /api/generate once, without streaming.
func ollamaGenerate(ctx context.Context, client *http.Client, server, model, prompt string, maxTokens int,
	temperature float64, schema map[string]any) (string, error) {
	body := map[string]any{
		"model": model, "prompt": prompt, "stream": false,
		"options": map[string]any{"num_predict": maxTokens, "temperature": temperature},
	}
	if schema != nil {
		body["format"] = schema
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/generate", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama answered %d", resp.StatusCode)
	}
	var out struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Response, nil
}

// followsSchema checks an answer against the subset of JSON Schema a plugin's json-schema
// is read with: type, properties, required, items, enum. Other keywords are not checked.
func followsSchema(answer string, schema map[string]any) bool {
	var v any
	dec := json.NewDecoder(strings.NewReader(answer))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return false
	}
	return matches(v, schema)
}

func matches(v any, schema map[string]any) bool {
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			eb, _ := json.Marshal(e)
			vb, _ := json.Marshal(v)
			if bytes.Equal(eb, vb) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	switch t, _ := schema["type"].(string); t {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if name, ok := r.(string); ok {
					if _, has := m[name]; !has {
						return false
					}
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			for name, sub := range props {
				sm, ok := sub.(map[string]any)
				if val, has := m[name]; has && ok && !matches(val, sm) {
					return false
				}
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return false
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for _, x := range a {
				if !matches(x, items) {
					return false
				}
			}
		}
	case "string":
		if _, ok := v.(string); !ok {
			return false
		}
	case "number":
		if _, ok := v.(json.Number); !ok {
			return false
		}
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		if _, err := n.Int64(); err != nil {
			return false
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return false
		}
	case "null":
		if v != nil {
			return false
		}
	}
	return true
}
