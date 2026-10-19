package extcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// WebSearchExtension is the web search behind "Find metadata" (catalogue:
// queues.extensionCatalog; the pipeline's enrich stage uses it).
const WebSearchExtension = "web_search"

// Where the web search keys are kept (secrets.ExtensionName).
var (
	GoogleKeySecret = secrets.ExtensionName(WebSearchExtension, "google_api_key")
	OllamaKeySecret = secrets.ExtensionName(WebSearchExtension, "ollama_api_key")
)

// The services the test asks (tests: a fake's address).
const (
	GoogleSearchURL = "https://www.googleapis.com/customsearch/v1"
	OllamaSearchURL = "https://ollama.com/api/web_search"
)

// CodeRateLimited: the search service's quota or rate limit is reached.
const CodeRateLimited = "rate_limited"

// WebSearch runs the Web search extension's Test connection: one real
// query to each configured backend (Google Programmable Search with the
// key and engine id; Ollama's web search with its key). Neither key is
// ever put in a detail, a log line or the answer: the Google key travels
// in the query string as the API requires, so only the classified error
// (never the transport's text, which names the URL) is reported.
type WebSearch struct {
	Deps
	// GoogleURL / OllamaURL override the services (tests); empty: the real ones.
	GoogleURL string
	OllamaURL string
}

// Test runs the checks: enabled, google, ollama, search (one of them works).
func (w *WebSearch) Test(ctx context.Context) (Result, error) {
	res := Result{CheckedAt: w.now()}
	settings, enabled, err := w.Ext.ExtensionSettings(ctx, WebSearchExtension)
	if err != nil {
		return res, err
	}
	add := func(l Line) { res.Lines = append(res.Lines, l) }
	if enabled {
		add(Line{Key: "enabled", OK: true, Detail: "on: Find metadata searches the web"})
	} else {
		add(Line{Key: "enabled", Optional: true, Code: CodeNotConfigured,
			Detail: "the extension is off: Find metadata uses Maktaba's own intel only (the checks below still run)"})
	}
	c := w.client()

	gKey, gCode, gDetail := w.secret(ctx, GoogleKeySecret)
	cx := str(settings, "google_cx")
	var google Line
	switch {
	case gKey == "" && gCode == CodeNoToken && cx == "":
		google = Line{Key: "google", Optional: true, Skipped: true, Detail: "no Google API key or engine id saved"}
	case gKey == "":
		google = Line{Key: "google", Code: gCode, Detail: gDetail}
	case cx == "":
		google = Line{Key: "google", Code: CodeNotConfigured, Detail: "no search engine id (cx) saved"}
	default:
		google = w.google(ctx, c, gKey, cx)
	}

	oKey, oCode, oDetail := w.secret(ctx, OllamaKeySecret)
	var ollama Line
	switch {
	case oKey == "" && oCode == CodeNoToken:
		ollama = Line{Key: "ollama", Optional: true, Skipped: true, Detail: "no Ollama API key saved (optional)"}
	case oKey == "":
		ollama = Line{Key: "ollama", Code: oCode, Detail: oDetail}
	default:
		ollama = w.ollama(ctx, c, oKey)
	}
	// one working backend is enough: the other one's failure is a warning
	if ollama.OK {
		google.Optional = true
	}
	if google.OK {
		ollama.Optional = true
	}
	add(google)
	add(ollama)

	switch {
	case google.OK || ollama.OK:
		which := "Google"
		if !google.OK {
			which = "Ollama"
		}
		add(Line{Key: "search", OK: true, Detail: which + " answers; rules.enrich.search picks the backend (auto: Google first)"})
	case google.Skipped && ollama.Skipped:
		add(Line{Key: "search", Code: CodeNotConfigured,
			Detail: "save a Google API key and engine id, or an Ollama API key"})
	default:
		add(Line{Key: "search", Code: firstCode(google, ollama), Detail: "no web search backend answers"})
	}
	finish(&res, "web search is ready")
	w.logResult(res)
	return res, nil
}

func firstCode(ls ...Line) string {
	for _, l := range ls {
		if l.Code != "" {
			return l.Code
		}
	}
	return CodeUnreachable
}

func (w *WebSearch) secret(ctx context.Context, name string) (string, string, string) {
	if w.Secrets == nil {
		return "", CodeSecrets, "the secrets store is not available on this server"
	}
	b, err := w.Secrets.Get(ctx, name)
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		return "", CodeNoToken, "no key saved"
	case errors.Is(err, secrets.ErrWrongKey):
		return "", CodeSecrets, "the saved key cannot be opened (secrets.key was replaced): save it again"
	case errors.Is(err, secrets.ErrNoKey):
		return "", CodeSecrets, "the secrets store is not available on this server"
	case err != nil:
		return "", CodeSecrets, "the saved key cannot be read"
	}
	k := strings.TrimSpace(string(b))
	if k == "" {
		return "", CodeNoToken, "no key saved"
	}
	return k, "", ""
}

// do sends one request bounded by the check timeout.
func (w *WebSearch) do(c *http.Client, req *http.Request) (answer, error) {
	start := time.Now()
	resp, err := c.Do(req)
	if err != nil {
		return answer{latency: time.Since(start)}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return answer{status: resp.StatusCode, body: body, latency: time.Since(start)}, err
}

type googleAnswer struct {
	SearchInformation struct {
		TotalResults string `json:"totalResults"`
	} `json:"searchInformation"`
	Items []struct {
		Link string `json:"link"`
	} `json:"items"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Errors  []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
	} `json:"error"`
}

func (w *WebSearch) google(ctx context.Context, c *http.Client, key, cx string) Line {
	raw := w.GoogleURL
	if raw == "" {
		raw = GoogleSearchURL
	}
	base, err := url.Parse(raw)
	if err != nil {
		return Line{Key: "google", Code: CodeBadURL, Detail: "the Google search address is not valid"}
	}
	q := url.Values{"key": {key}, "cx": {cx}, "q": {"Maktaba"}, "num": {"1"}}
	u := *base
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(ctx, w.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Line{Key: "google", Code: CodeBadURL, Detail: "the Google search request cannot be built"}
	}
	req.Header.Set("Accept", "application/json")
	a, err := w.do(c, req)
	if err != nil {
		code, det := classify(err, base, w.timeout())
		return Line{Key: "google", Code: code, Detail: Redact(det, key)}
	}
	l := Line{Key: "google", LatencyMS: ms(a.latency)}
	var g googleAnswer
	_ = json.Unmarshal(a.body, &g)
	reason := ""
	if g.Error != nil && len(g.Error.Errors) > 0 {
		reason = g.Error.Errors[0].Reason
	}
	switch {
	case a.status == http.StatusTooManyRequests || strings.Contains(reason, "Limit"):
		l.Code, l.Detail = CodeRateLimited, "Google's daily quota or rate limit is reached (the key works)"
	case a.status == http.StatusBadRequest && strings.Contains(strings.ToLower(reasonOr(g, reason)), "key"):
		l.Code, l.Detail = CodeTokenRejected, "Google refused the API key"
	case a.status == http.StatusBadRequest:
		l.Code, l.Detail = CodeNotFound, "Google refused the request: is the search engine id (cx) right?"
	case a.status == http.StatusForbidden || a.status == http.StatusUnauthorized:
		d := "Google refused the API key"
		if reason == "accessNotConfigured" || strings.Contains(reasonOr(g, reason), "has not been used") {
			d = "the Custom Search API is not enabled for this key's Google Cloud project"
		}
		l.Code, l.Detail = CodeTokenRejected, d
	case a.status >= 400:
		l.Code, l.Detail = CodeHTTP, fmt.Sprintf("Google answered HTTP %d", a.status)
	case g.SearchInformation.TotalResults == "" && g.Items == nil:
		l.Code, l.Detail = CodeBadAnswer, "Google answered, but not as the Custom Search API"
	default:
		l.OK = true
		l.Detail = fmt.Sprintf("Google answered in %d ms (%s results for a test query)",
			a.latency.Milliseconds(), orZero(g.SearchInformation.TotalResults))
	}
	l.Detail = Redact(l.Detail, key)
	return l
}

func reasonOr(g googleAnswer, reason string) string {
	if g.Error != nil {
		return reason + " " + g.Error.Message
	}
	return reason
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func (w *WebSearch) ollama(ctx context.Context, c *http.Client, key string) Line {
	raw := w.OllamaURL
	if raw == "" {
		raw = OllamaSearchURL
	}
	base, err := url.Parse(raw)
	if err != nil {
		return Line{Key: "ollama", Code: CodeBadURL, Detail: "the Ollama search address is not valid"}
	}
	ctx, cancel := context.WithTimeout(ctx, w.timeout())
	defer cancel()
	body, _ := json.Marshal(map[string]any{"query": "Maktaba", "max_results": 1})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return Line{Key: "ollama", Code: CodeBadURL, Detail: "the Ollama search request cannot be built"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	a, err := w.do(c, req)
	if err != nil {
		code, det := classify(err, base, w.timeout())
		return Line{Key: "ollama", Code: code, Detail: Redact(det, key)}
	}
	l := Line{Key: "ollama", LatencyMS: ms(a.latency)}
	var out struct {
		Results []json.RawMessage `json:"results"`
	}
	switch {
	case a.status == http.StatusUnauthorized || a.status == http.StatusForbidden:
		l.Code, l.Detail = CodeTokenRejected, "Ollama refused the API key"
	case a.status == http.StatusTooManyRequests:
		l.Code, l.Detail = CodeRateLimited, "Ollama's rate limit is reached (the key works)"
	case a.status >= 400:
		l.Code, l.Detail = CodeHTTP, fmt.Sprintf("Ollama answered HTTP %d", a.status)
	case json.Unmarshal(a.body, &out) != nil || out.Results == nil:
		l.Code, l.Detail = CodeBadAnswer, "Ollama answered, but not as its web search API"
	default:
		l.OK = true
		l.Detail = fmt.Sprintf("Ollama answered in %d ms (%d results for a test query)", a.latency.Milliseconds(), len(out.Results))
	}
	return l
}

func (w *WebSearch) logResult(res Result) {
	failed := []string{}
	for _, l := range res.Lines {
		if !l.OK && !l.Skipped {
			failed = append(failed, l.Key+"="+l.Code)
		}
	}
	w.log().Info("extensions: web search test connection", "ok", res.OK, "failed", strings.Join(failed, ","),
		"event", "extension_test")
}
