package logs

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// The log index's routes (Epic 53 story 53.09): search, live tail and "around" of this
// machine's logs, answered by the logs role (maktaba-server logs) on loopback and proxied
// here to admins only (RequireAdmin in Mount). With the role off or down the answer is 503
// log_index_unavailable: the Logs page then reads the segments (story 53.11).

// EnvLogsAddr is where the logs role answers, set by the supervisor.
const EnvLogsAddr = "MAKTABA_LOGS_ADDR"

// indexBase is the logs role's address ("" when this API runs without one).
func (h *Handler) indexBase() string {
	if h.IndexAddr != "" {
		return h.IndexAddr
	}
	return strings.TrimSpace(os.Getenv(EnvLogsAddr))
}

// proxyIndex sends the request's query to the role's path and copies its answer back
// (a tail streams).
func (h *Handler) proxyIndex(path string, stream bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base := h.indexBase()
		if base == "" {
			h.indexDown(w, r)
			return
		}
		p := path
		if fp := chi.URLParam(r, "fp"); fp != "" {
			p = strings.ReplaceAll(p, "{fp}", url.PathEscape(fp))
		}
		u := url.URL{Scheme: "http", Host: base, Path: p, RawQuery: r.URL.RawQuery}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
		if err != nil {
			httperror.Write(w, r, err)
			return
		}
		c := &http.Client{Timeout: 10 * time.Second}
		if stream {
			c = &http.Client{} // the tail lasts as long as the admin watches
		}
		resp, err := c.Do(req)
		if err != nil {
			h.indexDown(w, r)
			return
		}
		defer resp.Body.Close()
		for _, k := range []string{"Content-Type", "Cache-Control"} {
			if v := resp.Header.Get(k); v != "" {
				w.Header().Set(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if !stream {
			_, _ = io.Copy(w, resp.Body)
			return
		}
		fl, _ := w.(http.Flusher)
		buf := make([]byte, 32<<10)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				if fl != nil {
					fl.Flush()
				}
			}
			if err != nil {
				return
			}
		}
	}
}

func (h *Handler) indexDown(w http.ResponseWriter, r *http.Request) {
	httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.tv/problems/log-index-unavailable",
		Title: "log search is not running", Status: http.StatusServiceUnavailable,
		Detail: "the log index (logs.index) is off or restarting; recent logs can be read from the files",
		Extras: map[string]any{"code": "log_index_unavailable"}})
}
