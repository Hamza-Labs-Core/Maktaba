package agent

import (
	"bufio"
	"bytes"
	"container/heap"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// History endpoints (design-agent.md §4, §5): they read the cluster files
// (the share, or the local fallback), merging a day's per-process files by
// timestamp. Nothing comes from the database.
//
//	GET /api/cluster/files
//	GET /api/cluster/files/{ip}/logs?date&level&q&limit&before
//	GET /api/cluster/files/{ip}/jobs?date&stage&event&limit&before
//	GET /api/cluster/files/{ip}/llm?date&limit&before
//	GET /api/cluster/files/{ip}/events?date&limit&before
//	GET /api/cluster/files/{ip}/metrics?date&from&to&limit
//	GET /api/cluster/files/{ip}/queues?date&from&to&limit
//	GET /api/cluster/files/{ip}/library?date&from&to&limit

const (
	maxLineBytes     = 4 << 20
	maxProcessFiles  = 500
	maxProcessBytes  = 256 << 10
	defaultRecords   = 200
	maxRecords       = 5000
	defaultSamples   = 20_000
	maxSamples       = 100_000
	minSamplesPerLog = 100
)

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	return requireAdminP(w, r)
}

// MachineFiles is one machine folder in GET /api/cluster/files.
type MachineFiles struct {
	IP        string              `json:"ip"`
	Processes []json.RawMessage   `json:"processes"`
	Dates     map[string][]string `json:"dates"`
}

// FilesIndex is GET /api/cluster/files.
type FilesIndex struct {
	Files Location       `json:"files"`
	IPs   []MachineFiles `json:"ips"`
}

// isPlainDir / isPlainFile refuse symlinks: the share is writable by every
// machine, and a link must not lead a reader out of the cluster folder.
func isPlainDir(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode().IsDir()
}

func isPlainFile(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode().IsRegular()
}

// Files implements GET /api/cluster/files.
func (h *Hub) Files(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	loc := h.Location()
	out := FilesIndex{Files: loc, IPs: []MachineFiles{}}
	entries, err := os.ReadDir(loc.Dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		httperror.Write(w, r, httperror.Internal("read the cluster folder"))
		return
	}
	for _, e := range entries {
		if !ValidIPDir(e.Name()) || !isPlainDir(filepath.Join(loc.Dir, e.Name())) {
			continue
		}
		ipPath := filepath.Join(loc.Dir, e.Name())
		m := MachineFiles{IP: e.Name(), Processes: readProcesses(filepath.Join(ipPath, "processes")), Dates: map[string][]string{}}
		for _, kind := range DayKinds {
			m.Dates[kind] = listDates(filepath.Join(ipPath, kind))
		}
		out.IPs = append(out.IPs, m)
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func readProcesses(dir string) []json.RawMessage {
	out := []json.RawMessage{}
	if !isPlainDir(dir) {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if len(out) >= maxProcessFiles {
			break
		}
		p := filepath.Join(dir, e.Name())
		if !strings.HasSuffix(e.Name(), ".json") || !isPlainFile(p) {
			continue
		}
		b, err := readCapped(p, maxProcessBytes)
		if err != nil || !json.Valid(b) {
			continue
		}
		out = append(out, json.RawMessage(bytes.TrimSpace(b)))
	}
	return out
}

func readCapped(p string, n int64) ([]byte, error) {
	f, err := os.Open(p) //nolint:gosec // a validated path under the cluster folder
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, n+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > n {
		return nil, errors.New("too large")
	}
	return b, nil
}

func listDates(dir string) []string {
	out := []string{}
	if !isPlainDir(dir) {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if ValidDate(e.Name()) && isPlainDir(filepath.Join(dir, e.Name())) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// dayFile is one process's file for a day.
type dayFile struct {
	path     string
	workerID string
}

// dayFiles lists <dir>/<ip>/<kind>/<date>/*.jsonl.
func dayFiles(dir, ip, kind, date string) []dayFile {
	day := filepath.Join(dir, ip, kind, date)
	for _, p := range []string{filepath.Join(dir, ip), filepath.Join(dir, ip, kind), day} {
		if !isPlainDir(p) {
			return nil
		}
	}
	entries, err := os.ReadDir(day)
	if err != nil {
		return nil
	}
	var out []dayFile
	for _, e := range entries {
		name := e.Name()
		p := filepath.Join(day, name)
		if !strings.HasSuffix(name, ".jsonl") || !isPlainFile(p) {
			continue
		}
		out = append(out, dayFile{path: p, workerID: strings.TrimSuffix(name, ".jsonl")})
	}
	return out
}

// eachLine calls fn for every line of a file; lines longer than
// maxLineBytes are skipped.
func eachLine(p string, fn func(line []byte)) {
	f, err := os.Open(p) //nolint:gosec // a validated path under the cluster folder
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(f, 64<<10)
	var long []byte
	skipping := false
	for {
		chunk, err := br.ReadSlice('\n')
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			if !skipping {
				long = append(long, chunk...)
				if len(long) > maxLineBytes {
					long, skipping = nil, true
				}
			}
			continue
		case err != nil && !errors.Is(err, io.EOF):
			return
		}
		if !skipping {
			line := chunk
			if long != nil {
				line = append(long, chunk...)
			}
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				fn(line)
			}
		}
		long, skipping = nil, false
		if err != nil {
			return
		}
	}
}

// parseTS reads a record's ts: RFC 3339 (a naive time is UTC) or Unix
// seconds.
func parseTS(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC(), true
			}
		}
		return time.Time{}, false
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil && f > 0 {
		sec := int64(f)
		return time.Unix(sec, int64((f-float64(sec))*1e9)).UTC(), true
	}
	return time.Time{}, false
}

type record struct {
	ts   time.Time
	data json.RawMessage
}

// recHeap keeps the newest records (a min-heap on ts).
type recHeap []record

func (h recHeap) Len() int           { return len(h) }
func (h recHeap) Less(i, j int) bool { return h[i].ts.Before(h[j].ts) }
func (h recHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *recHeap) Push(x any)        { *h = append(*h, x.(record)) }
func (h *recHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// withWorker adds worker_id to a record object.
func withWorker(m map[string]json.RawMessage, workerID string) json.RawMessage {
	if workerID != "" {
		if _, has := m["worker_id"]; !has {
			b, _ := json.Marshal(workerID)
			m["worker_id"] = b
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

type historyQuery struct {
	ip, kind, date string
	limit          int
	before         *time.Time
	from, to       *time.Time
}

func badRequest(w http.ResponseWriter, r *http.Request, msg string) {
	httperror.Write(w, r, httperror.BadRequest(msg))
}

func parseTimeParam(v string) (*time.Time, bool) {
	if v == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return nil, false
	}
	t = t.UTC()
	return &t, true
}

func (h *Hub) parseHistory(w http.ResponseWriter, r *http.Request, def, max int) (historyQuery, bool) {
	q := historyQuery{ip: chi.URLParam(r, "ip"), kind: chi.URLParam(r, "kind"), date: r.URL.Query().Get("date")}
	if !ValidIPDir(q.ip) {
		badRequest(w, r, "ip must be an address (IPv6 with '-') or a host name")
		return q, false
	}
	if q.date == "" {
		q.date = h.now().Format("2006-01-02")
	}
	if !ValidDate(q.date) {
		badRequest(w, r, "date must be YYYY-MM-DD")
		return q, false
	}
	q.limit = def
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			badRequest(w, r, "limit must be a positive number")
			return q, false
		}
		q.limit = min(n, max)
	}
	var ok bool
	if q.before, ok = parseTimeParam(r.URL.Query().Get("before")); !ok {
		badRequest(w, r, "before must be an RFC 3339 time")
		return q, false
	}
	if q.from, ok = parseTimeParam(r.URL.Query().Get("from")); !ok {
		badRequest(w, r, "from must be an RFC 3339 time")
		return q, false
	}
	if q.to, ok = parseTimeParam(r.URL.Query().Get("to")); !ok {
		badRequest(w, r, "to must be an RFC 3339 time")
		return q, false
	}
	return q, true
}

// History implements GET /api/cluster/files/{ip}/{kind}.
func (h *Hub) History(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	switch chi.URLParam(r, "kind") {
	case "logs", "jobs", "llm", "events":
		h.historyRecords(w, r)
	case "metrics", "queues", "library":
		h.historySamples(w, r)
	default:
		httperror.Write(w, r, httperror.NotFound("history kind"))
	}
}

func (h *Hub) historyRecords(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseHistory(w, r, defaultRecords, maxRecords)
	if !ok {
		return
	}
	qs := r.URL.Query()
	minRank := 0
	if v := qs.Get("level"); v != "" {
		if minRank = levelRank(v); minRank == 0 {
			badRequest(w, r, "level must be debug, info, warning or error")
			return
		}
	}
	needle := []byte(strings.ToLower(qs.Get("q")))
	stage, event := qs.Get("stage"), qs.Get("event")
	dir := h.Location().Dir

	keep := &recHeap{}
	more := false
	consider := func(line []byte, workerID string, onlyIP string) {
		if len(needle) > 0 && !bytes.Contains(bytes.ToLower(line), needle) {
			return
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(line, &m) != nil {
			return
		}
		ts, ok := parseTS(m["ts"])
		if !ok || (q.before != nil && !ts.Before(*q.before)) {
			return
		}
		if onlyIP != "" && jsonString(m["ip"]) != onlyIP && IPDir(jsonString(m["ip"])) != onlyIP {
			return
		}
		if minRank > 0 {
			rk := levelRank(jsonString(m["level"]))
			if rk == 0 {
				rk = levelRank("info")
			}
			if rk < minRank {
				return
			}
		}
		if stage != "" && jsonString(m["stage"]) != stage {
			return
		}
		if event != "" && jsonString(m["event"]) != event {
			return
		}
		if keep.Len() >= q.limit {
			if !ts.After((*keep)[0].ts) {
				more = true
				return
			}
			heap.Pop(keep)
			more = true
		}
		if data := withWorker(m, workerID); data != nil {
			heap.Push(keep, record{ts: ts, data: data})
		}
	}
	for _, f := range dayFiles(dir, q.ip, q.kind, q.date) {
		wid := f.workerID
		if q.kind == KindEvents && wid == "hub" {
			wid = ""
		}
		eachLine(f.path, func(line []byte) { consider(line, wid, "") })
	}
	// the hub files a node's connection events under the home's folder
	if home := IPDir(h.homeIP); q.kind == KindEvents && q.ip != home {
		for _, f := range dayFiles(dir, home, KindEvents, q.date) {
			if f.workerID == "hub" {
				eachLine(f.path, func(line []byte) { consider(line, "", q.ip) })
			}
		}
	}
	recs := make([]record, keep.Len())
	copy(recs, *keep)
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].ts.Before(recs[j].ts) })
	out := make([]json.RawMessage, 0, len(recs))
	for _, rec := range recs {
		out = append(out, rec.data)
	}
	var cursor *string
	if more && len(recs) > 0 {
		c := recs[0].ts.Format(time.RFC3339Nano)
		cursor = &c
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"ip": q.ip, "date": q.date, "records": out, "before": cursor})
}

func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// historySamples answers metrics / queues / library: every sample in
// [from, to), oldest first, evenly thinned per file past the limit.
func (h *Hub) historySamples(w http.ResponseWriter, r *http.Request) {
	q, ok := h.parseHistory(w, r, defaultSamples, maxSamples)
	if !ok {
		return
	}
	dir := h.Location().Dir
	files := dayFiles(dir, q.ip, q.kind, q.date)
	perFile := q.limit
	if len(files) > 1 {
		perFile = max(q.limit/len(files), minSamplesPerLog)
	}
	var all []record
	thinned := false
	for _, f := range files {
		var kept []record
		stride, n := 1, 0
		eachLine(f.path, func(line []byte) {
			var head struct {
				TS json.RawMessage `json:"ts"`
			}
			if json.Unmarshal(line, &head) != nil {
				return
			}
			ts, ok := parseTS(head.TS)
			if !ok || (q.from != nil && ts.Before(*q.from)) || (q.to != nil && !ts.Before(*q.to)) {
				return
			}
			n++
			if (n-1)%stride != 0 {
				return
			}
			kept = append(kept, record{ts: ts, data: append(json.RawMessage(nil), line...)})
			if len(kept) >= 2*perFile {
				// halve: keep every other sample, double the stride
				half := kept[:0]
				for i := 0; i < len(kept); i += 2 {
					half = append(half, kept[i])
				}
				kept, stride = half, stride*2
				thinned = true
			}
		})
		wid := f.workerID
		if wid == "hub" {
			wid = ""
		}
		for _, k := range kept {
			if q.kind == "metrics" {
				b, _ := json.Marshal(map[string]any{"ts": k.ts.Format(time.RFC3339Nano), "worker_id": wid, "metrics": k.data})
				k.data = b
			}
			all = append(all, k)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ts.Before(all[j].ts) })
	out := make([]json.RawMessage, 0, len(all))
	for _, rec := range all {
		out = append(out, rec.data)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"ip": q.ip, "date": q.date, "samples": out, "thinned": thinned})
}
