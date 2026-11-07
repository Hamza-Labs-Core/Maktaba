package plugins

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
)

// Routes are the supervisor's loopback plugin routes (story 54.38 §4), behind its
// per-boot token like /components/:
//
//	POST /plugins/install              the .mkplugin as the body: 200 Installed, or 4xx {error, detail, reason}
//	POST /plugins/{id}/discard?folder= remove a folder the API refused (never current or kept)
//
// An install is answered when it is done (a package is at most 50 MB packed): no
// progress route. [plugins] install = "off" (54.10) answers 409 plugins_install_off.
type Routes struct {
	Files *Files
	// Token is the per-boot token ("": every request 403).
	Token func() string
	// Off is [plugins] install = "off".
	Off bool
	// Keep answers the folders of id plugin_installs names (current and previous): never
	// discarded.
	Keep func(id string) []string
}

func (rt *Routes) authorized(r *http.Request) bool {
	tok := ""
	if rt.Token != nil {
		tok = rt.Token()
	}
	return tok != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+tok)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Handler adds /plugins/ to base.
func (rt *Routes) Handler(base http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("/plugins/", func(w http.ResponseWriter, r *http.Request) {
		if !rt.authorized(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if rt.Files == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "plugins_unavailable"})
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/plugins/")
		switch {
		case rest == "install" && r.Method == http.MethodPost:
			rt.install(w, r)
		case strings.HasSuffix(rest, "/discard") && r.Method == http.MethodPost:
			id := strings.TrimSuffix(rest, "/discard")
			if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
				http.NotFound(w, r)
				return
			}
			var keep []string
			if rt.Keep != nil {
				keep = rt.Keep(id)
			}
			removed, err := rt.Files.Discard(r.Context(), id, r.URL.Query().Get("folder"), keep...)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plugin_discard_failed", "detail": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": id, "removed": removed})
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

func (rt *Routes) install(w http.ResponseWriter, r *http.Request) {
	if rt.Off {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "plugins_install_off",
			"detail": "[plugins] install = \"off\" in server.toml"})
		return
	}
	body := http.MaxBytesReader(w, r.Body, componentpack.PluginMaxPackedBytes+1<<20)
	in, err := rt.Files.Install(r.Context(), body)
	var ref *Refusal
	switch {
	case errors.As(err, &ref):
		status := http.StatusBadRequest
		if ref.Code == ReasonBusy {
			status = http.StatusConflict
		}
		if ref.Code == ReasonDiskSpace {
			status = http.StatusInsufficientStorage
		}
		writeJSON(w, status, ref)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "plugin_install_failed", "detail": err.Error()})
	default:
		writeJSON(w, http.StatusOK, in)
	}
}
