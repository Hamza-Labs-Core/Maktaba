package plugins

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// Routes are the supervisor's loopback plugin routes (story 54.38 §4), behind its
// per-boot token like /components/:
//
//	POST /plugins/install              the .mkplugin as the body: 200 Installed, or 4xx {error, detail, reason}
//	POST /plugins/{id}/discard?folder= remove a folder the API refused (never current or kept)
//	POST /plugins/entitlements/verify  check entitlements offline (story 54.19 §6) against
//	                                   the store keys this machine verified: 200 {valid, refused}
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
		case rest == "entitlements/verify" && r.Method == http.MethodPost:
			rt.verifyEntitlements(w, r)
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

// EntitlementsRequest is what the API asks the supervisor to check: the envelopes the store
// answered, and this home's store id and key fingerprint.
type EntitlementsRequest struct {
	HomeID       string            `json:"home_id"`
	HomeKeyFP    string            `json:"home_key_fp"`
	Entitlements []json.RawMessage `json:"entitlements"`
}

// CheckedEntitlement is one entitlement that holds.
type CheckedEntitlement struct {
	PluginID   string          `json:"plugin_id"`
	PurchaseID string          `json:"purchase_id"`
	Product    string          `json:"product"`
	ValidFrom  string          `json:"valid_from"`
	ValidUntil string          `json:"valid_until"`
	IssuedAt   string          `json:"issued_at"`
	Envelope   json.RawMessage `json:"envelope"`
}

// RefusedEntitlement is one that does not, by its place in the request.
type RefusedEntitlement struct {
	Index int    `json:"index"`
	Code  string `json:"code"`
	Step  string `json:"step"`
}

// maxEntitlements is the most one request checks (a home holds one per paid plugin).
const maxEntitlements = 1000

func (rt *Routes) verifyEntitlements(w http.ResponseWriter, r *http.Request) {
	var in EntitlementsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&in); err != nil ||
		in.HomeID == "" || in.HomeKeyFP == "" || len(in.Entitlements) > maxEntitlements {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "entitlements_bad_request"})
		return
	}
	t := rt.Files.trust()
	keys := t.StoreKeys()
	if keys == nil {
		// no verified store-keys.json on this machine (a placeholder root, or none fetched):
		// nothing can be checked, and the API keeps what it has
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store_keys_unavailable"})
		return
	}
	out := struct {
		Valid   []CheckedEntitlement `json:"valid"`
		Refused []RefusedEntitlement `json:"refused"`
	}{Valid: []CheckedEntitlement{}, Refused: []RefusedEntitlement{}}
	st, now := t.State(), t.now()
	for i, raw := range in.Entitlements {
		e, err := pluginsig.CheckEntitlement(raw, keys, st, now, in.HomeID, in.HomeKeyFP)
		if err != nil {
			code, step := pluginsig.CodeOf(err)
			out.Refused = append(out.Refused, RefusedEntitlement{Index: i, Code: code, Step: step})
			continue
		}
		out.Valid = append(out.Valid, CheckedEntitlement{PluginID: e.PluginID, PurchaseID: e.PurchaseID, Product: e.Product,
			ValidFrom: pluginsig.FormatTime(e.ValidFrom), ValidUntil: pluginsig.FormatTime(e.ValidUntil),
			IssuedAt: pluginsig.FormatTime(e.IssuedAt), Envelope: raw})
	}
	writeJSON(w, http.StatusOK, out)
}
