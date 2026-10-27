package logs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// FileSource reads a machine's own log files (server.log, app.log, launcher.log: where an
// update's story is) over a window. node "" is this machine's, from its supervisor's
// /logs/files; else that machine's, over the machine channel (logs.files). q carries
// files, since, until and limit, already checked. It answers the supervisor's JSON
// ({entries, files}).
type FileSource func(ctx context.Context, node string, q url.Values) (json.RawMessage, error)

// FileSource errors.
var (
	// ErrNodeOffline: the machine's agent is not connected to the home.
	ErrNodeOffline = errors.New("the machine's agent is not connected")
	// ErrNodeNoAnswer: the machine did not answer, or refused.
	ErrNodeNoAnswer = errors.New("the machine did not answer")
	// ErrNoSupervisor: this api runs without the maktaba-server supervisor (no files).
	ErrNoSupervisor = errors.New("not running under the maktaba-server supervisor")
)

// fileNames are the files a query may name.
var fileNames = map[string]bool{"server": true, "app": true, "launcher": true}

// maxFileLines bounds a query (the supervisor's own bound too).
const maxFileLines = 10000

// nodeIDRe is a machine's id as a query takes it.
var nodeIDRe = regexp.MustCompile(`^[0-9A-Za-z-]{1,64}$`)

// fileQuery checks a request's files, since, until and limit, and keeps only those.
func fileQuery(in url.Values) (url.Values, error) {
	out := url.Values{}
	if v := strings.TrimSpace(in.Get("files")); v != "" {
		for _, f := range strings.Split(v, ",") {
			if !fileNames[strings.TrimSpace(f)] {
				return nil, errors.New("files: server, app or launcher")
			}
		}
		out.Set("files", v)
	}
	for _, k := range []string{"since", "until"} {
		if v := strings.TrimSpace(in.Get(k)); v != "" {
			if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
				return nil, errors.New(k + ": not an RFC 3339 time")
			}
			out.Set(k, v)
		}
	}
	if v := strings.TrimSpace(in.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, errors.New("limit: a positive number")
		}
		out.Set("limit", strconv.Itoa(min(n, maxFileLines)))
	}
	return out, nil
}

func codeErr(e *httperror.Error, code string) *httperror.Error {
	e.Type = "about:blank#" + code
	return e.With("code", code)
}

// Files serves GET /api/admin/logs/files: a machine's server.log, app.log and
// launcher.log over a window (?files=&since=&until=&limit=&node=), entries oldest first.
func (h *Handler) Files(w http.ResponseWriter, r *http.Request) {
	q, err := fileQuery(r.URL.Query())
	if err != nil {
		httperror.Write(w, r, codeErr(httperror.BadRequest(err.Error()), "bad_request"))
		return
	}
	node := strings.TrimSpace(r.URL.Query().Get("node"))
	if node != "" && !nodeIDRe.MatchString(node) {
		httperror.Write(w, r, codeErr(httperror.BadRequest("node: a machine's id"), "bad_request"))
		return
	}
	raw, err := h.files(r.Context(), node, q)
	switch {
	case errors.Is(err, ErrNodeOffline):
		e := codeErr(httperror.Unavailable(30), "node_offline")
		e.Detail = err.Error()
		httperror.Write(w, r, e)
		return
	case errors.Is(err, ErrNodeNoAnswer):
		httperror.Write(w, r, codeErr(httperror.BadGateway("the machine did not answer").Wrap(err), "node_no_answer"))
		return
	case err != nil:
		httperror.Write(w, r, codeErr(httperror.BadGateway("the supervisor did not answer").Wrap(err), "supervisor_unreachable"))
		return
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil || doc == nil {
		httperror.Write(w, r, codeErr(httperror.BadGateway("the log files' answer is not JSON"), "supervisor_unreachable"))
		return
	}
	nb, _ := json.Marshal(node)
	doc["node"] = nb
	common.WriteJSON(w, r, http.StatusOK, doc)
}

// files asks the source; with none (or no supervisor), no file is there.
func (h *Handler) files(ctx context.Context, node string, q url.Values) (json.RawMessage, error) {
	if h.FileSource != nil {
		raw, err := h.FileSource(ctx, node, q)
		if !errors.Is(err, ErrNoSupervisor) {
			return raw, err
		}
	}
	if node != "" {
		return nil, ErrNodeOffline
	}
	names := []string{"server", "app", "launcher"}
	if v := q.Get("files"); v != "" {
		names = strings.Split(v, ",")
	}
	files := make([]map[string]any, 0, len(names))
	for _, n := range names {
		files = append(files, map[string]any{"name": strings.TrimSpace(n) + ".log", "missing": true, "truncated": false})
	}
	return json.Marshal(map[string]any{"entries": []any{}, "files": files})
}

// fileEntry is the part of a line the bundle keeps.
type fileEntry struct {
	File string `json:"file"`
	Raw  string `json:"raw"`
}

// bundleFiles are a machine's files over the request's window for the bundle, by name
// under prefix (files/server.log, …); a machine that cannot answer leaves a note.
func (h *Handler) bundleFiles(ctx context.Context, node, prefix string, in url.Values) map[string][]byte {
	q, err := fileQuery(url.Values{"since": {in.Get("since")}, "until": {in.Get("until")}})
	if err != nil {
		q = url.Values{}
	}
	q.Set("limit", strconv.Itoa(maxFileLines))
	out := map[string][]byte{}
	raw, err := h.files(ctx, node, q)
	if err != nil {
		out[prefix+"files-unavailable.txt"] = []byte(err.Error() + "\n")
		return out
	}
	var doc struct {
		Entries []fileEntry `json:"entries"`
		Files   []struct {
			Name    string `json:"name"`
			Missing bool   `json:"missing"`
		} `json:"files"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		out[prefix+"files-unavailable.txt"] = []byte("the log files' answer is not JSON\n")
		return out
	}
	for _, f := range doc.Files {
		if !f.Missing && strings.HasSuffix(f.Name, ".log") && !strings.ContainsAny(f.Name, `/\`) {
			out[prefix+"files/"+f.Name] = []byte{}
		}
	}
	for _, e := range doc.Entries {
		k := prefix + "files/" + e.File
		if _, ok := out[k]; ok {
			out[k] = append(append(out[k], e.Raw...), '\n')
		}
	}
	return out
}
