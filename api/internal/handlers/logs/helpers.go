package logs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// errorSummary deduplicates the error-level entries from a ring slice
// into per-message buckets with occurrence counts, newest-first by
// count. The bundle's error-summary.json is what a support engineer
// scans first, so the dedup keeps a retry-storm from drowning the
// signal.
func errorSummary(entries []mlog.Entry) ErrorSummary {
	type agg struct {
		count    int
		service  string
		lastSeen string
	}
	buckets := map[string]*agg{}
	total := 0
	for _, e := range entries {
		if mlog.ParseLevel(e.Level) < errorLevel {
			continue
		}
		var rec struct {
			Msg string `json:"msg"`
			Ts  string `json:"ts"`
		}
		_ = json.Unmarshal(e.Raw, &rec)
		key := rec.Msg
		total++
		b := buckets[key]
		if b == nil {
			b = &agg{service: e.Service}
			buckets[key] = b
		}
		b.count++
		// Entries arrive oldest→newest, so the last write wins as the
		// most-recent timestamp.
		b.lastSeen = rec.Ts
	}

	out := ErrorSummary{TotalErrors: total, UniqueErrors: len(buckets)}
	for msg, b := range buckets {
		out.Errors = append(out.Errors, ErrorBucket{
			Message:  msg,
			Service:  b.service,
			Count:    b.count,
			LastSeen: b.lastSeen,
		})
	}
	sort.Slice(out.Errors, func(i, j int) bool {
		if out.Errors[i].Count != out.Errors[j].Count {
			return out.Errors[i].Count > out.Errors[j].Count
		}
		return out.Errors[i].Message < out.Errors[j].Message
	})
	if out.Errors == nil {
		out.Errors = []ErrorBucket{}
	}
	return out
}

// errorLevel is slog.LevelError without importing slog here.
const errorLevel = 8

// scopeJSONL restricts a block of newline-delimited JSON log lines to a
// single user: it keeps lines attributed to scopeUserID plus
// un-attributed system lines (no user_id), mirroring scopedEntries.
// Used to scope proxied peer logs, which arrive as raw JSONL the peer
// service cannot filter by user. A line that does not parse as JSON is
// dropped (fail-closed) so a malformed peer line can never leak through
// the user scope.
func scopeJSONL(data []byte, scopeUserID string) []byte {
	if len(data) == 0 {
		return data
	}
	var buf bytes.Buffer
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.UserID == "" || rec.UserID == scopeUserID {
			buf.Write(line)
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

// bufferRecorder is a minimal http.ResponseWriter that captures a
// handler's body so we can embed the aggregator's /api/system/health
// JSON into the bundle without a network round-trip. Kept local rather
// than pulling httptest into the production build.
type bufferRecorder struct {
	header http.Header
	body   *bytes.Buffer
	status int
}

func newBufferRecorder() *bufferRecorder {
	return &bufferRecorder{header: http.Header{}, body: &bytes.Buffer{}}
}

func (b *bufferRecorder) Header() http.Header { return b.header }

func (b *bufferRecorder) Write(p []byte) (int, error) { return b.body.Write(p) }

func (b *bufferRecorder) WriteHeader(status int) { b.status = status }
