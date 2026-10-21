package llmserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultRegistry is where Ollama's own library lives (the manifests give a
// model's size before it is downloaded).
const DefaultRegistry = "https://registry.ollama.ai"

// Client talks to one Ollama server (its HTTP API) and to the registry.
type Client struct {
	URL  string       // the server, e.g. http://127.0.0.1:11533
	HTTP *http.Client // short requests (version, tags, manifests)
	// Pulls has no overall timeout: a 5 GB download takes as long as it takes.
	Pulls *http.Client
	// Registry replaces DefaultRegistry for models of Ollama's library (tests).
	Registry string
}

// NewClient is a client of the server at url.
func NewClient(url string) *Client {
	return &Client{
		URL:   strings.TrimRight(url, "/"),
		HTTP:  &http.Client{Timeout: 10 * time.Second},
		Pulls: &http.Client{},
	}
}

// Version is the server's version (GET /api/version): it answers.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.getJSON(ctx, c.HTTP, c.URL+"/api/version", nil, &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// Have is the set of models the server has (GET /api/tags), by their names.
func (c *Client) Have(ctx context.Context) (map[string]bool, error) {
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := c.getJSON(ctx, c.HTTP, c.URL+"/api/tags", nil, &tags); err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, m := range tags.Models {
		for _, n := range []string{m.Name, m.Model} {
			if n != "" {
				have[Canonical(n)] = true
			}
		}
	}
	return have, nil
}

// Canonical is a model name as Ollama stores it: "qwen2.5" is "qwen2.5:latest".
func Canonical(name string) string {
	name = strings.TrimSpace(name)
	last := name[strings.LastIndex(name, "/")+1:]
	if !strings.Contains(last, ":") {
		name += ":latest"
	}
	return name
}

// Progress is one pull's state: bytes of every layer seen so far.
type Progress struct {
	Status string
	Done   int64
	Total  int64
}

// Pull downloads model (POST /api/pull, streamed), calling fn on every
// update. Ollama resumes a partial download by itself, so a pull that stopped
// (a restart, a lost connection) continues where it was.
func (c *Client) Pull(ctx context.Context, model string, fn func(Progress)) error {
	body, _ := json.Marshal(map[string]any{"model": model, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Pulls.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("pull %s: %s: %s", model, resp.Status, apiError(b))
	}
	type layer struct{ done, total int64 }
	layers := map[string]*layer{}
	var order []string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	success := false
	for sc.Scan() {
		var ev struct {
			Status    string `json:"status"`
			Digest    string `json:"digest"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Error != "" {
			return fmt.Errorf("pull %s: %s", model, ev.Error)
		}
		if ev.Digest != "" {
			l := layers[ev.Digest]
			if l == nil {
				l = &layer{}
				layers[ev.Digest] = l
				order = append(order, ev.Digest)
			}
			if ev.Total > 0 {
				l.total = ev.Total
			}
			if ev.Completed > l.done {
				l.done = ev.Completed
			}
		}
		if ev.Status == "success" {
			success = true
		}
		if fn != nil {
			p := Progress{Status: ev.Status}
			for _, d := range order {
				p.Done += layers[d].done
				p.Total += layers[d].total
			}
			fn(p)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("pull %s: %w", model, err)
	}
	if !success {
		return fmt.Errorf("pull %s: the server stopped before it finished", model)
	}
	return nil
}

// Size is the bytes model takes once downloaded, from its registry manifest
// (the config and every layer), before anything is downloaded.
func (c *Client) Size(ctx context.Context, model string) (int64, error) {
	u, err := c.manifestURL(model)
	if err != nil {
		return 0, err
	}
	var m struct {
		Config struct {
			Size int64 `json:"size"`
		} `json:"config"`
		Layers []struct {
			Size int64 `json:"size"`
		} `json:"layers"`
	}
	h := map[string]string{"Accept": "application/vnd.docker.distribution.manifest.v2+json"}
	if err := c.getJSON(ctx, c.HTTP, u, h, &m); err != nil {
		return 0, err
	}
	n := m.Config.Size
	for _, l := range m.Layers {
		n += l.Size
	}
	if n <= 0 {
		return 0, fmt.Errorf("manifest of %s lists no layers", model)
	}
	return n, nil
}

// manifestURL: "qwen2.5:7b" is library/qwen2.5 tag 7b on Ollama's registry;
// "user/model" is that user's; "host.tld/ns/model:tag" is on that host.
func (c *Client) manifestURL(model string) (string, error) {
	name, tag, ok := strings.Cut(Canonical(model), ":")
	if !ok || name == "" || tag == "" {
		return "", fmt.Errorf("bad model name %q", model)
	}
	host := strings.TrimRight(c.Registry, "/")
	if host == "" {
		host = DefaultRegistry
	}
	parts := strings.Split(name, "/")
	if len(parts) > 1 && strings.Contains(parts[0], ".") {
		host = "https://" + parts[0]
		parts = parts[1:]
	}
	if len(parts) == 1 {
		parts = []string{"library", parts[0]}
	}
	return host + "/v2/" + strings.Join(parts, "/") + "/manifests/" + tag, nil
}

func (c *Client) getJSON(ctx context.Context, hc *http.Client, url string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s: %s", url, resp.Status, apiError(b))
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	return nil
}

// apiError is Ollama's {"error": "..."} message, else the body's start.
func apiError(b []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return e.Error
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// ErrNotRunning: nothing answers at the server's address.
var ErrNotRunning = errors.New("the model server is not running")
