package queues

// Every library's YouTube sources from Tartube's records (slot 0175):
//
//	POST /api/queues/youtube-tartube   {path, database?}: queue the import (stage youtube_tartube)
//	GET  /api/queues/youtube-tartube   the last import (its state and report) and the path to offer
//
// The pipeline reads Tartube's export or database (tartube_import's stub-only reader),
// makes each channel or playlist whose download folder is a library's root a source of
// that library, off, and seeds what Tartube downloaded; its report is the job's
// output_summary. Nothing is turned on and no library is made.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// StageYouTubeTartube is the pipeline's Tartube import (catalog subject).
const StageYouTubeTartube = "youtube_tartube"

// catalogSubject is the one catalog subject (as the TV and Plex stages use it).
const catalogSubject = "00000000-0000-0000-0000-00000000ca7a"

// TartubeImportRequest is the body of POST /api/queues/youtube-tartube.
type TartubeImportRequest struct {
	Path     string `json:"path"`
	Database string `json:"database"`
}

// TartubeImport is the last Tartube import, and the file to offer for the next.
type TartubeImport struct {
	JobID         *string         `json:"job_id"`
	State         *string         `json:"state"`
	Error         *string         `json:"error"`
	CreatedAt     *time.Time      `json:"created_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	Path          *string         `json:"path"`
	Report        json.RawMessage `json:"report"`
	SuggestedPath *string         `json:"suggested_path"`
}

// ImportTartubeSources implements POST /api/queues/youtube-tartube.
func (h *Handler) ImportTartubeSources(w http.ResponseWriter, r *http.Request) {
	var req TartubeImportRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if e := (TartubeRequest{Path: req.Path, Database: req.Database}).validate(); e != nil {
		httperror.Write(w, r, e)
		return
	}
	spec := map[string]any{"path": req.Path}
	if req.Database != "" {
		spec["database"] = req.Database
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	p := SourceRefreshPriority
	er, err := Enqueue(ctx, tx, EnqueueParams{SubjectKind: KindCatalog, SubjectID: catalogSubject,
		Stage: StageYouTubeTartube, Priority: &p, Force: true, Payload: map[string]any{"tartube": spec},
		Reason: ReasonFor(ctx, "import tartube sources", nil)})
	if err != nil {
		writeErr(w, r, "enqueue", err)
		return
	}
	var job string
	if er.JobID != nil {
		job = strconv.FormatInt(*er.JobID, 10)
	}
	if err := auditAdmin(ctx, tx, "queue.youtube_tartube.import", "catalog", "", map[string]any{
		"path": req.Path, "database": req.Database, "job_id": job, "outcome": er.Outcome}); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "commit", err)
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"job_id": er.JobID, "outcome": er.Outcome})
}

// TartubeSources implements GET /api/queues/youtube-tartube.
func (h *Handler) TartubeSources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var out TartubeImport
	var id int64
	var state string
	var jerr sql.NullString
	var created time.Time
	var finished sql.NullTime
	var payload, summary []byte
	err := h.DB.QueryRowContext(ctx, `SELECT id, state, error, created_at, finished_at,
	       COALESCE(payload, 'null'::jsonb), COALESCE(output_summary, 'null'::jsonb)
	  FROM processing_jobs WHERE stage = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
		StageYouTubeTartube).Scan(&id, &state, &jerr, &created, &finished, &payload, &summary)
	switch {
	case err == sql.ErrNoRows:
		out.Report = json.RawMessage("null")
	case err != nil:
		writeErr(w, r, "tartube import", err)
		return
	default:
		s := strconv.FormatInt(id, 10)
		out.JobID, out.State, out.Error, out.CreatedAt = &s, &state, nullStrPtr(jerr), &created
		out.FinishedAt = nullTimePtr(finished)
		out.Report = json.RawMessage(summary)
		var p struct {
			Tartube struct {
				Path string `json:"path"`
			} `json:"tartube"`
		}
		if json.Unmarshal(payload, &p) == nil && p.Tartube.Path != "" {
			out.Path = &p.Tartube.Path
		}
	}
	if out.SuggestedPath, err = h.suggestTartube(ctx); err != nil {
		writeErr(w, r, "libraries", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// tartubeLookTimeout bounds the search for a tartube.db (a NAS mount may not answer).
var tartubeLookTimeout = 3 * time.Second

// suggestTartube is a tartube.db in the folder that holds the most library roots (Tartube
// downloads each channel into a folder of its own there), in the database's form; nil
// when there is none.
func (h *Handler) suggestTartube(ctx context.Context) (*string, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT r FROM libraries, unnest(roots) r WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	count := map[string]int{}
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			rows.Close()
			return nil, err
		}
		root = strings.TrimRight(root, "/")
		if dir := path.Dir(root); dir != "." && dir != "/" && dir != root {
			count[dir]++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(count))
	for d := range count {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool {
		if count[dirs[i]] != count[dirs[j]] {
			return count[dirs[i]] > count[dirs[j]]
		}
		return dirs[i] < dirs[j]
	})
	if len(dirs) > 5 {
		dirs = dirs[:5]
	}
	ch := make(chan *string, 1)
	go func() {
		defer safego.Recover(nil, "youtube_tartube_suggest")
		for _, d := range dirs {
			for _, name := range []string{"tartube.db", "tartube_db_export.json"} {
				if fi, err := os.Stat(filepath.Join(h.local(d), name)); err == nil && fi.Mode().IsRegular() {
					p := d + "/" + name
					ch <- &p
					return
				}
			}
		}
		ch <- nil
	}()
	select {
	case p := <-ch:
		return p, nil
	case <-time.After(tartubeLookTimeout):
		return nil, nil
	}
}
