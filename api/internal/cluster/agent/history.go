package agent

import (
	"bufio"
	"bytes"
	"compress/gzip"
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
//	GET /api/cluster/files/{ip}/logs?date&level&q&worker_id&limit&before
//	GET /api/cluster/files/{ip}/jobs?date&stage&event&worker_id&limit&before
//
// (worker= is accepted for worker_id.) Logs and metrics come from the
// database cache when it covers the range (header X-Maktaba-Source: cache),
// else from the files (files).
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

// MachineFiles is one machine folder in GET /api/cluster/files: the days
// the share holds per kind, and the days the home's archive holds
// (archived_dates, §7.10; a day may be in both).
type MachineFiles struct {
	IP            string              `json:"ip"`
	Processes     []json.RawMessage   `json:"processes"`
	Dates         map[string][]string `json:"dates"`
	ArchivedDates map[string][]string `json:"archived_dates"`
}

// FilesIndex is GET /api/cluster/files.
type FilesIndex struct {
	Files Location       `json:"files"`
	Nodes []MachineFiles `json:"nodes"`
}

// Readers go through an os.Root at the cluster folder and refuse links
// (plainDirs / plainFile): the share is writable by every machine, and a
// link must not lead a reader out of the cluster folder.

// openCluster opens the cluster folder; nil when it does not exist yet.
func openCluster(dir string) (*os.Root, error) {
	if dir == "" {
		return nil, nil
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return root, err
}

// Files implements GET /api/cluster/files.
func (h *Hub) Files(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	loc := h.Location()
	out := FilesIndex{Files: loc, Nodes: []MachineFiles{}}
	root, err := openCluster(loc.Dir)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read the cluster folder"))
		return
	}
	if root != nil {
		defer func() { _ = root.Close() }()
	}
	arch := h.archiveRoot()
	if arch != nil {
		defer func() { _ = arch.Close() }()
	}
	byIP := map[string]*MachineFiles{}
	var order []string
	machine := func(ip string) *MachineFiles {
		m := byIP[ip]
		if m == nil {
			m = &MachineFiles{IP: ip, Processes: []json.RawMessage{}, Dates: map[string][]string{},
				ArchivedDates: map[string][]string{}}
			for _, kind := range DayKinds {
				m.Dates[kind] = []string{}
				m.ArchivedDates[kind] = []string{}
			}
			byIP[ip] = m
			order = append(order, ip)
		}
		return m
	}
	for _, src := range []struct {
		root     *os.Root
		archived bool
	}{{root, false}, {arch, true}} {
		if src.root == nil {
			continue
		}
		entries, err := readDirIn(src.root, ".")
		if err != nil {
			if !src.archived {
				httperror.Write(w, r, httperror.Internal("read the cluster folder"))
				return
			}
			continue
		}
		for _, e := range entries {
			if !ValidIPDir(e.Name()) || plainDirs(src.root, e.Name()) != nil {
				continue
			}
			m := machine(e.Name())
			for _, kind := range DayKinds {
				days := listDates(src.root, filepath.Join(e.Name(), kind))
				if src.archived {
					m.ArchivedDates[kind] = days
				} else {
					m.Dates[kind] = days
				}
			}
			if !src.archived {
				m.Processes = readProcesses(src.root, filepath.Join(e.Name(), "processes"))
			}
		}
	}
	sort.Strings(order)
	for _, ip := range order {
		out.Nodes = append(out.Nodes, *byIP[ip])
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func readProcesses(root *os.Root, dir string) []json.RawMessage {
	out := []json.RawMessage{}
	entries, err := readDirIn(root, dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if len(out) >= maxProcessFiles {
			break
		}
		p := filepath.Join(dir, e.Name())
		if !strings.HasSuffix(e.Name(), ".json") || !plainFile(root, p) {
			continue
		}
		b, err := readCapped(root, p, maxProcessBytes)
		if err != nil || !json.Valid(b) {
			continue
		}
		out = append(out, json.RawMessage(bytes.TrimSpace(b)))
	}
	return out
}

func readCapped(root *os.Root, p string, n int64) ([]byte, error) {
	f, err := root.Open(p)
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

func listDates(root *os.Root, dir string) []string {
	out := []string{}
	entries, err := readDirIn(root, dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if ValidDate(e.Name()) && plainDirs(root, filepath.Join(dir, e.Name())) == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// dayFile is one process's file for a day: relative to root (the share,
// or the archive when gz).
type dayFile struct {
	root     *os.Root
	path     string
	workerID string
	gz       bool
}

// each calls fn for every line of the file (decompressed when gz).
func (f dayFile) each(fn func(line []byte)) {
	if !f.gz {
		eachLine(f.root, f.path, fn)
		return
	}
	if !plainFile(f.root, f.path) {
		return
	}
	in, err := f.root.Open(f.path)
	if err != nil {
		return
	}
	defer func() { _ = in.Close() }()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return
	}
	defer func() { _ = zr.Close() }()
	eachLineIn(zr, fn)
}

// dayData lists a day's files: the share's while it holds them, else the
// archive's; it says which ("files" or "archive").
func dayData(share, arch *os.Root, ip, kind, date string) ([]dayFile, string) {
	if fs := dayFiles(share, ip, kind, date); len(fs) > 0 {
		return fs, "files"
	}
	if fs := archivedDayFiles(arch, ip, kind, date); len(fs) > 0 {
		return fs, "archive"
	}
	return nil, "files"
}

// archiveRoot opens the archive for reading (nil without one).
func (h *Hub) archiveRoot() *os.Root {
	if h.cfg.Archive == nil {
		return nil
	}
	return openArchiveRead(h.cfg.Archive.Dir)
}

// dayFiles lists <ip>/<kind>/<date>/*.jsonl.
func dayFiles(root *os.Root, ip, kind, date string) []dayFile {
	if root == nil {
		return nil
	}
	day := filepath.Join(ip, kind, date)
	entries, err := readDirIn(root, day)
	if err != nil {
		return nil
	}
	var out []dayFile
	for _, e := range entries {
		name := e.Name()
		p := filepath.Join(day, name)
		if !strings.HasSuffix(name, ".jsonl") || !plainFile(root, p) {
			continue
		}
		out = append(out, dayFile{root: root, path: p, workerID: strings.TrimSuffix(name, ".jsonl")})
	}
	return out
}

// eachLine calls fn for every line of a file; lines longer than
// maxLineBytes are skipped.
func eachLine(root *os.Root, p string, fn func(line []byte)) {
	if !plainFile(root, p) {
		return
	}
	f, err := root.Open(p)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	eachLineIn(f, fn)
}

// eachLineIn is eachLine over a reader.
func eachLineIn(r io.Reader, fn func(line []byte)) {
	br := bufio.NewReaderSize(r, 64<<10)
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
		if !skipping && len(long)+len(chunk) <= maxLineBytes+1 {
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

func (h *Hub) parseHistory(w http.ResponseWriter, r *http.Request, def, ceiling int) (historyQuery, bool) {
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
		q.limit = min(n, ceiling)
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
	worker := qs.Get("worker_id")
	if worker == "" {
		worker = qs.Get("worker")
	}
	if q.kind == "logs" && h.logsFromCache(w, r, q, worker, qs.Get("level"), qs.Get("q")) {
		return
	}
	root, err := openCluster(h.Location().Dir)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read the cluster folder"))
		return
	}
	if root != nil {
		defer func() { _ = root.Close() }()
	}
	arch := h.archiveRoot()
	if arch != nil {
		defer func() { _ = arch.Close() }()
	}

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
	files, source := dayData(root, arch, q.ip, q.kind, q.date)
	for _, f := range files {
		if worker != "" && f.workerID != worker {
			continue
		}
		wid := f.workerID
		if q.kind == KindEvents && wid == "hub" {
			wid = ""
		}
		f.each(func(line []byte) { consider(line, wid, "") })
	}
	// the hub files a node's connection events under the home's folder
	if home := IPDir(h.homeIP); q.kind == KindEvents && q.ip != home {
		hubFiles, hubSource := dayData(root, arch, home, KindEvents, q.date)
		if len(files) == 0 {
			source = hubSource
		}
		for _, f := range hubFiles {
			if f.workerID == "hub" {
				f.each(func(line []byte) { consider(line, "", q.ip) })
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
	w.Header().Set(sourceHeader, source)
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"ip": q.ip, "date": q.date, "records": out, "next_before": cursor})
}

// sourceHeader says where a history answer came from: cache, files (the
// share) or archive (the home's archive, §7.10).
const sourceHeader = "X-Maktaba-Source"

// dayRange is [the day's start, the next day's start) for a YYYY-MM-DD.
func dayRange(date string) (time.Time, time.Time) {
	d, _ := time.Parse("2006-01-02", date)
	return d, d.AddDate(0, 0, 1)
}

// cacheRange is the day's range as far as it has happened: [the day's
// start, min(the next day's start, now)). A cache never claims what it
// does not hold, so asking it for the rest of today would always miss.
func (h *Hub) cacheRange(date string) (time.Time, time.Time) {
	from, to := dayRange(date)
	if now := h.now(); now.Before(to) {
		to = now
	}
	return from, to
}

// logsFromCache answers a logs query from the cache when it covers the
// range; it reports whether it answered.
func (h *Hub) logsFromCache(w http.ResponseWriter, r *http.Request, q historyQuery, worker, level, needle string) bool {
	if h.cfg.Cache == nil {
		return false
	}
	from, to := h.cacheRange(q.date)
	if q.before != nil && q.before.Before(to) {
		to = *q.before
	}
	if !to.After(from) || !h.cfg.Cache.Covers("logs", from, to) {
		return false
	}
	recs, err := h.cfg.Cache.ReadLogs(r.Context(), CacheQuery{IP: q.ip, WorkerID: worker, Level: normLevel(level),
		Q: needle, From: from, To: to, Limit: q.limit + 1})
	if err != nil {
		h.log().Warn("cluster: cache read failed; reading the files", "err", err, "event", "cluster_cache_read_failed")
		return false
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].TS.Before(recs[j].TS) })
	more := len(recs) > q.limit
	if more {
		recs = recs[len(recs)-q.limit:]
	}
	out := make([]json.RawMessage, 0, len(recs))
	for _, rec := range recs {
		var m map[string]json.RawMessage
		if json.Unmarshal(rec.Line, &m) != nil {
			continue
		}
		if data := withWorker(m, rec.WorkerID); data != nil {
			out = append(out, data)
		}
	}
	var cursor *string
	if more && len(recs) > 0 {
		c := recs[0].TS.UTC().Format(time.RFC3339Nano)
		cursor = &c
	}
	w.Header().Set(sourceHeader, "cache")
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"ip": q.ip, "date": q.date, "records": out, "next_before": cursor})
	return true
}

// metricsFromCache answers a metrics query from the cache when it covers
// the range; it reports whether it answered.
func (h *Hub) metricsFromCache(w http.ResponseWriter, r *http.Request, q historyQuery) bool {
	if h.cfg.Cache == nil {
		return false
	}
	from, to := h.cacheRange(q.date)
	if q.from != nil && q.from.After(from) {
		from = *q.from
	}
	if q.to != nil && q.to.Before(to) {
		to = *q.to
	}
	if !to.After(from) || !h.cfg.Cache.Covers("metrics", from, to) {
		return false
	}
	// the cache thins evenly in SQL to at most limit rows (a day of one IP
	// is 8 640 frames per process); `thinned` then reports only the hub's
	// own thinning, which a cache that honours Limit never needs
	recs, err := h.cfg.Cache.ReadMetrics(r.Context(), CacheQuery{IP: q.ip, From: from, To: to, Limit: q.limit})
	if err != nil {
		h.log().Warn("cluster: cache read failed; reading the files", "err", err, "event", "cluster_cache_read_failed")
		return false
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].TS.Before(recs[j].TS) })
	stride, thinned := 1, false
	if len(recs) > q.limit {
		stride = (len(recs) + q.limit - 1) / q.limit
		thinned = true
	}
	out := make([]json.RawMessage, 0, len(recs)/stride+1)
	for i := 0; i < len(recs); i += stride {
		b, err := json.Marshal(map[string]any{"ts": recs[i].TS.UTC().Format(time.RFC3339Nano), "worker_id": recs[i].WorkerID,
			"metrics": recs[i].Line})
		if err == nil {
			out = append(out, b)
		}
	}
	w.Header().Set(sourceHeader, "cache")
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"ip": q.ip, "date": q.date, "samples": out, "thinned": thinned})
	return true
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
	if q.kind == "metrics" && h.metricsFromCache(w, r, q) {
		return
	}
	root, err := openCluster(h.Location().Dir)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read the cluster folder"))
		return
	}
	if root != nil {
		defer func() { _ = root.Close() }()
	}
	arch := h.archiveRoot()
	if arch != nil {
		defer func() { _ = arch.Close() }()
	}
	files, source := dayData(root, arch, q.ip, q.kind, q.date)
	perFile := q.limit
	if len(files) > 1 {
		perFile = max(q.limit/len(files), minSamplesPerLog)
	}
	var all []record
	thinned := false
	for _, f := range files {
		var kept []record
		stride, n := 1, 0
		f.each(func(line []byte) {
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
	w.Header().Set(sourceHeader, source)
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"ip": q.ip, "date": q.date, "samples": out, "thinned": thinned})
}
