package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logfiles"
)

// channelLogBytes bounds a node's answer to logs.files: the machine channel's frames are
// at most 4 MB sealed (agent.MaxFrame), so the oldest lines of a big window are left out.
const channelLogBytes = 2_500_000

// logFilesAdmin adds GET /logs/files to the supervisor's loopback admin (the per-boot
// local token as a bearer): this machine's server.log, app.log and launcher.log over a
// window (internal/logfiles), which the api serves at /api/admin/logs/files and puts in
// the diagnostics bundle.
func logFilesAdmin(base http.Handler, token string, dirs logfiles.Dirs) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("/logs/files", func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		q, err := logfiles.ParseQuery(r.URL.Query().Get)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(logfiles.Read(dirs, q))
	})
	return mux
}

// logFilesCommand answers the home's logs.files on the machine channel (a node's files,
// for its logs page and its bundle): {files?, since?, until?, limit?}.
func logFilesCommand(_ context.Context, dirs logfiles.Dirs, args json.RawMessage) (ok bool, detail any) {
	var a struct {
		Files string `json:"files"`
		Since string `json:"since"`
		Until string `json:"until"`
		Limit string `json:"limit"`
	}
	if len(args) > 0 && json.Unmarshal(args, &a) != nil {
		return false, "bad arguments"
	}
	q, err := logfiles.ParseQuery(func(k string) string {
		return map[string]string{"files": a.Files, "since": a.Since, "until": a.Until, "limit": a.Limit}[k]
	})
	if err != nil {
		return false, err.Error()
	}
	return true, logfiles.Fit(logfiles.Read(dirs, q), channelLogBytes)
}
