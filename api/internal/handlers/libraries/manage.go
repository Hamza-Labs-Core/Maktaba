// Epic 45 story 45.2: the admin's Manage libraries page.
//
//	GET /api/libraries/manage   every library with its folders, counts,
//	                            last scan and the folders it shares
//	GET /api/libraries/folders  the folder picker over this machine's disk
//
// Both are admin-only and refuse anyone else before any query.
package libraries

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/libpaths"
)

// ManagedRoot is one library folder as this machine sees it.
type ManagedRoot struct {
	Path      string  `json:"path"`
	LocalPath string  `json:"local_path"`
	Reachable bool    `json:"reachable"`
	Problem   *string `json:"problem"`
}

// LibraryCounts groups the library's videos by state. Processing is every
// state a video passes through on its way to ready; superseded videos count
// only in Total.
type LibraryCounts struct {
	Total      int `json:"total"`
	Missing    int `json:"missing"`
	Failed     int `json:"failed"`
	Ready      int `json:"ready"`
	Processing int `json:"processing"`
}

// ScanSummary is the library's latest scan job.
type ScanSummary struct {
	JobID      string     `json:"job_id"`
	State      string     `json:"state"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Error      *string    `json:"error"`
}

// RootConflict is a folder this library shares with another one.
type RootConflict struct {
	Root        string `json:"root"`
	OtherRoot   string `json:"other_root"`
	LibraryID   string `json:"library_id"`
	LibraryName string `json:"library_name"`
	Kind        string `json:"kind"` // "same" | "nested"
	OtherCount  int    `json:"other_count"`
}

// ManagedLibrary is one row of the Manage libraries page.
type ManagedLibrary struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Roots     []ManagedRoot   `json:"roots"`
	Settings  json.RawMessage `json:"settings"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Counts    LibraryCounts   `json:"counts"`
	LastScan  *ScanSummary    `json:"last_scan"`
	Scanning  bool            `json:"scanning"`
	Conflicts []RootConflict  `json:"conflicts"`
}

// ConflictMember is a library of a conflict group.
type ConflictMember struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Videos int    `json:"videos"`
}

// ConflictGroup is a set of libraries joined by shared folders, with the one
// to merge the others into: the one holding the most videos.
type ConflictGroup struct {
	Libraries       []ConflictMember `json:"libraries"`
	Kind            string           `json:"kind"` // "same" when any two share a folder, else "nested"
	SuggestedTarget string           `json:"suggested_target"`
}

// ManageResponse is GET /api/libraries/manage.
type ManageResponse struct {
	Items          []ManagedLibrary `json:"items"`
	ConflictGroups []ConflictGroup  `json:"conflict_groups"`
}

// liveJobStates are the processing_jobs states of a job not yet finished
// (the slot 0058 one-live-scan index uses the same five).
var liveJobStates = map[string]bool{
	"pending": true, "claimed": true, "running": true, "resuming": true, "paused": true,
}

// requireAdmin answers 403 to anyone but an admin and reports whether the
// caller may go on.
func requireAdmin(w http.ResponseWriter, r *http.Request) (*principal.Principal, bool) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return nil, false
	}
	return p, true
}

// Manage lists every library for the management page: three queries
// whatever the number of libraries (the libraries, one GROUP BY over videos,
// the latest scan per library), then the folder checks on this machine.
func (h *Handler) Manage(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	ctx := r.Context()
	libs, err := h.allLibraries(ctx)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load libraries").Wrap(err))
		return
	}
	counts, err := h.videoCounts(ctx)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("count videos").Wrap(err))
		return
	}
	scans, err := h.latestScans(ctx)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load scans").Wrap(err))
		return
	}

	var roots []string
	for _, l := range libs {
		roots = append(roots, l.Roots...)
	}
	problems := h.checkRoots(roots)

	resp := ManageResponse{Items: make([]ManagedLibrary, 0, len(libs)), ConflictGroups: []ConflictGroup{}}
	for _, l := range libs {
		m := ManagedLibrary{
			ID: l.ID, Name: l.Name, Settings: l.Settings, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
			Roots: []ManagedRoot{}, Counts: counts[l.ID], Conflicts: []RootConflict{},
		}
		for _, root := range l.Roots {
			mr := ManagedRoot{Path: root, LocalPath: libpaths.ToLocal(root), Reachable: true}
			if e := problems[root]; e != nil {
				msg := e.Error()
				mr.Reachable, mr.Problem = false, &msg
			}
			m.Roots = append(m.Roots, mr)
		}
		if s, ok := scans[l.ID]; ok {
			s := s
			m.LastScan = &s
			m.Scanning = liveJobStates[s.State]
		}
		resp.Items = append(resp.Items, m)
	}
	resp.ConflictGroups = findConflicts(resp.Items)
	common.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) allLibraries(ctx context.Context) ([]Library, error) {
	rows, err := h.DB.QueryContext(ctx, `
		SELECT id, name, roots, settings, created_at, updated_at FROM libraries ORDER BY name, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		var roots stringArray
		var settings []byte
		if err := rows.Scan(&l.ID, &l.Name, &roots, &settings, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		l.Roots = roots
		if len(settings) == 0 {
			settings = []byte(`{}`)
		}
		l.Settings = settings
		out = append(out, l)
	}
	return out, rows.Err()
}

// videoCounts is one GROUP BY over videos (the (library_id, state) index
// answers it) for every library at once.
func (h *Handler) videoCounts(ctx context.Context) (map[string]LibraryCounts, error) {
	rows, err := h.DB.QueryContext(ctx, `
		SELECT library_id, state, COUNT(*) FROM videos GROUP BY library_id, state
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]LibraryCounts{}
	for rows.Next() {
		var lib, state string
		var n int
		if err := rows.Scan(&lib, &state, &n); err != nil {
			return nil, err
		}
		c := out[lib]
		c.Total += n
		switch state {
		case "ready", "ready_no_audio":
			c.Ready += n
		case "failed", "corrupted":
			c.Failed += n
		case "missing":
			c.Missing += n
		case "superseded":
		default:
			c.Processing += n
		}
		out[lib] = c
	}
	return out, rows.Err()
}

// latestScans is each library's newest scan job, by created_at.
func (h *Handler) latestScans(ctx context.Context) (map[string]ScanSummary, error) {
	rows, err := h.DB.QueryContext(ctx, `
		SELECT library_id, id, state, created_at, finished_at, error FROM (
			SELECT library_id, id, state, created_at, finished_at, error,
			       ROW_NUMBER() OVER (PARTITION BY library_id ORDER BY created_at DESC, id DESC) AS rn
			FROM processing_jobs
			WHERE stage = 'scan' AND library_id IS NOT NULL
		) j WHERE rn = 1
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ScanSummary{}
	for rows.Next() {
		var lib string
		var id int64
		var s ScanSummary
		var finished sql.NullTime
		var jobErr sql.NullString
		if err := rows.Scan(&lib, &id, &s.State, &s.CreatedAt, &finished, &jobErr); err != nil {
			return nil, err
		}
		s.JobID = strconv.FormatInt(id, 10)
		if finished.Valid {
			t := finished.Time
			s.FinishedAt = &t
		}
		if jobErr.Valid && jobErr.String != "" {
			e := jobErr.String
			s.Error = &e
		}
		out[lib] = s
	}
	return out, rows.Err()
}

// rootCheckTimeout bounds the folder checks: a NAS mount that stopped
// answering would otherwise hold the page. A variable for the tests.
var rootCheckTimeout = 3 * time.Second

// hungRootFor is how long a root that timed out is reported as "timeout"
// without being checked again: the abandoned check may still be stuck on the
// mount, and every page load would add another one.
const hungRootFor = time.Minute

var errRootTimeout = errors.New("timeout")

// hungRoots are the roots whose check timed out lately, with when to try
// them again. One set for the process: the mounts are the machine's.
var hungRoots = struct {
	sync.Mutex
	until map[string]time.Time
}{until: map[string]time.Time{}}

// checkRoots runs the PathChecker on every root at once and returns the
// failing ones; a root that has not answered in time is "timeout", and stays
// so for hungRootFor without a new check.
func (h *Handler) checkRoots(roots []string) map[string]error {
	checker := h.PathChecker
	if checker == nil {
		checker = OSPathChecker{}
	}
	type result struct {
		root string
		err  error
	}
	out := map[string]error{}
	now := h.now()
	uniq := map[string]bool{}
	hungRoots.Lock()
	for _, r := range roots {
		if until, ok := hungRoots.until[r]; ok {
			if now.Before(until) {
				out[r] = errRootTimeout
				continue
			}
			delete(hungRoots.until, r)
		}
		uniq[r] = true
	}
	hungRoots.Unlock()
	ch := make(chan result, len(uniq))
	sem := make(chan struct{}, 16)
	for root := range uniq {
		go func(root string) {
			sem <- struct{}{}
			defer func() { <-sem }()
			ch <- result{root, checker.Check(root)}
		}(root)
	}
	pending := len(uniq)
	timer := time.NewTimer(rootCheckTimeout)
	defer timer.Stop()
	for pending > 0 {
		select {
		case res := <-ch:
			pending--
			delete(uniq, res.root)
			if res.err != nil {
				out[res.root] = res.err
			}
		case <-timer.C:
			hungRoots.Lock()
			for root := range uniq {
				out[root] = errRootTimeout
				hungRoots.until[root] = now.Add(hungRootFor)
			}
			hungRoots.Unlock()
			return out
		}
	}
	return out
}

// conflictKind compares two roots as stored, then as this machine reaches
// them (one library may name the NAS's path and another the local mount):
// "same", "nested" or "".
func conflictKind(a, b string) string {
	k := overlapKind(a, b)
	if k == "" {
		if la, lb := libpaths.ToLocal(a), libpaths.ToLocal(b); la != a || lb != b {
			k = overlapKind(la, lb)
		}
	}
	switch k {
	case "":
		return ""
	case kindSame:
		return "same"
	default:
		return "nested"
	}
}

// findConflicts fills each library's conflicts and returns the groups of
// libraries joined by them, each with its suggested target.
func findConflicts(items []ManagedLibrary) []ConflictGroup {
	parent := make([]int, len(items))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	same := map[int]bool{}
	for i := range items {
		for j := range items {
			if i == j {
				continue
			}
			for _, a := range items[i].Roots {
				for _, b := range items[j].Roots {
					kind := conflictKind(a.Path, b.Path)
					if kind == "" {
						continue
					}
					items[i].Conflicts = append(items[i].Conflicts, RootConflict{
						Root: a.Path, OtherRoot: b.Path, LibraryID: items[j].ID, LibraryName: items[j].Name,
						Kind: kind, OtherCount: items[j].Counts.Total,
					})
					if kind == "same" {
						same[i] = true
					}
					parent[find(i)] = find(j)
				}
			}
		}
	}
	groups := map[int][]int{}
	var order []int
	for i := range items {
		if len(items[i].Conflicts) == 0 {
			continue
		}
		g := find(i)
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], i)
	}
	out := []ConflictGroup{}
	for _, g := range order {
		members := groups[g]
		cg := ConflictGroup{Kind: "nested"}
		best := -1
		for _, i := range members {
			cg.Libraries = append(cg.Libraries, ConflictMember{ID: items[i].ID, Name: items[i].Name, Videos: items[i].Counts.Total})
			if same[i] {
				cg.Kind = "same"
			}
			// the most videos wins; on a tie the older library
			if best < 0 || items[i].Counts.Total > items[best].Counts.Total ||
				(items[i].Counts.Total == items[best].Counts.Total && items[i].CreatedAt.Before(items[best].CreatedAt)) {
				best = i
			}
		}
		cg.SuggestedTarget = items[best].ID
		out = append(out, cg)
	}
	return out
}

// Place is a starting folder of the picker.
type Place struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	LocalPath string `json:"local_path"`
}

// FolderEntry is one sub-folder in a listing.
type FolderEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FolderListing is GET /api/libraries/folders?path=.
type FolderListing struct {
	Path      string        `json:"path"`
	LocalPath string        `json:"local_path"`
	Parent    *string       `json:"parent"`
	Entries   []FolderEntry `json:"entries"`
	Truncated bool          `json:"truncated"`
}

// maxFolderEntries caps a listing; a folder of more sub-folders is cut and
// marked truncated.
const maxFolderEntries = 2000

// EnvMediaRoots is the server's [media] roots, comma separated.
const EnvMediaRoots = "MAKTABA_MEDIA_ROOTS"

// Folders is the folder picker. It lists this machine's disk (the server's,
// where the libraries are scanned) and names every folder in the database's
// form, the form a library stores.
func (h *Handler) Folders(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("path"))
	if q == "" {
		places, err := h.places(r.Context())
		if err != nil {
			httperror.Write(w, r, httperror.Internal("load libraries").Wrap(err))
			return
		}
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"places": places})
		return
	}

	local := libpaths.ToLocal(q)
	if !filepath.IsAbs(local) {
		httperror.Write(w, r, httperror.NotFound("folder "+q))
		return
	}
	local = filepath.Clean(local)
	places, err := h.places(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load libraries").Wrap(err))
		return
	}
	// the picker lists only folders inside its places, never the rest of the
	// disk (a system folder is no library)
	if !insidePlaces(local, places) {
		httperror.Write(w, r, httperror.NotFound("folder "+q))
		return
	}
	// a mount that stopped answering would hold the request (and the
	// listing's goroutine) forever: the listing runs on its own and the
	// request gives up on it after folderListTimeout
	type listed struct {
		names []string
		err   error
	}
	ch := make(chan listed, 1)
	go func() {
		names, err := listFolders(local)
		ch <- listed{names, err}
	}()
	timer := time.NewTimer(folderListTimeout)
	defer timer.Stop()
	var names []string
	select {
	case res := <-ch:
		if res.err != nil {
			// not there, not a folder or not readable: the picker cannot open it
			httperror.Write(w, r, httperror.NotFound("folder "+q))
			return
		}
		names = res.names
	case <-timer.C:
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/folder-timeout",
			Title:  "folder did not answer",
			Status: http.StatusGatewayTimeout,
			Detail: "folder " + q + " did not answer in time; its drive or share may be disconnected",
		})
		return
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.ToLower(names[i]), strings.ToLower(names[j])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	out := FolderListing{Path: libpaths.ToDB(local), LocalPath: local, Entries: []FolderEntry{}}
	if len(names) > maxFolderEntries {
		names, out.Truncated = names[:maxFolderEntries], true
	}
	for _, name := range names {
		out.Entries = append(out.Entries, FolderEntry{Name: name, Path: libpaths.ToDB(filepath.Join(local, name))})
	}
	if parent := filepath.Dir(local); parent != local && insidePlaces(parent, places) {
		p := libpaths.ToDB(parent)
		out.Parent = &p
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// folderListTimeout bounds a folder listing. A variable for the tests.
var folderListTimeout = 5 * time.Second

// listFolders is the sub-folders of local, hidden ones left out. A variable
// so the tests can stand in a mount that does not answer.
var listFolders = func(local string) ([]string, error) {
	ents, err := os.ReadDir(local)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			// /Volumes holds its mounts as links
			if fi, err := os.Stat(filepath.Join(local, name)); err == nil && fi.IsDir() {
				isDir = true
			}
		}
		if isDir {
			names = append(names, name)
		}
	}
	return names, nil
}

// insidePlaces reports whether the clean absolute path local is one of the
// places or inside one.
func insidePlaces(local string, places []Place) bool {
	for _, p := range places {
		base := filepath.Clean(p.LocalPath)
		if local == base || strings.HasPrefix(local, strings.TrimSuffix(base, string(filepath.Separator))+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// places are the picker's starting folders, in this order: the path map's
// targets, the server's media roots, the folders holding the libraries,
// /Volumes on macOS and the home folder. Only folders this machine can open
// are listed, each once.
func (h *Handler) places(ctx context.Context) ([]Place, error) {
	var locals []string
	for _, p := range libpaths.Pairs() {
		locals = append(locals, p.Local)
	}
	for _, root := range strings.Split(os.Getenv(EnvMediaRoots), ",") {
		if root = strings.TrimSpace(root); root != "" {
			locals = append(locals, libpaths.ToLocal(root))
		}
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT roots FROM libraries`)
	if err != nil {
		return nil, err
	}
	var parents []string
	for rows.Next() {
		var roots stringArray
		if err := rows.Scan(&roots); err != nil {
			rows.Close()
			return nil, err
		}
		for _, root := range roots {
			if local := libpaths.ToLocal(root); filepath.IsAbs(local) {
				parents = append(parents, filepath.Dir(filepath.Clean(local)))
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(parents)
	locals = append(locals, parents...)
	if runtime.GOOS == "darwin" {
		locals = append(locals, "/Volumes")
	}
	if home, err := os.UserHomeDir(); err == nil {
		locals = append(locals, home)
	}

	out := []Place{}
	seen := map[string]bool{}
	for _, local := range locals {
		if !filepath.IsAbs(local) {
			continue
		}
		local = filepath.Clean(local)
		if seen[local] {
			continue
		}
		seen[local] = true
		if fi, err := os.Stat(local); err != nil || !fi.IsDir() {
			continue
		}
		path := libpaths.ToDB(local)
		name := filepath.Base(filepath.FromSlash(path))
		if name == string(filepath.Separator) || name == "." {
			name = path
		}
		out = append(out, Place{Name: name, Path: path, LocalPath: local})
	}
	return out, nil
}
