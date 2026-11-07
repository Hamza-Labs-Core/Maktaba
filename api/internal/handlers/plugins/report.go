package plugins

// Rating and reporting a plugin from inside the home (story 54.18 §3, §4), admin only. The
// home passes them to the store signed with its home store key; a home not linked to a store
// account cannot rate (409 store_not_linked) and reports from the listing page instead. The
// home sends the plugin's id and version and nothing else: no library data, no logs.
//
//	POST /api/plugins/{id}/rate    {stars, text}
//	POST /api/plugins/{id}/report  {reason, details}

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

func (h *Admin) toStore(w http.ResponseWriter, r *http.Request, path string, fields map[string]any, installed bool) {
	if !h.store(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	version := ""
	// a rating needs the plugin installed here; a report may be of one seen on Browse
	if d, err := h.L.Show(r.Context(), id); err == nil {
		version = d.Version
	} else if installed {
		h.fail(w, r, err)
		return
	}
	st, err := h.Store.LoadState(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if st == nil {
		httperror.Write(w, r, problem(http.StatusConflict, "store_not_linked", "link this home to a store account first"))
		return
	}
	fields["plugin"], fields["version"] = id, version
	body, _ := json.Marshal(fields)
	resp, raw, err := h.Store.Signed(r.Context(), http.MethodPost, "/v1/homes/"+st.HomeID+path, body)
	if err != nil {
		h.storeFail(w, r, err)
		return
	}
	var ans map[string]any
	_ = json.Unmarshal(raw, &ans)
	if resp.StatusCode >= 300 {
		code, _ := ans["error"].(string)
		if code == "" {
			code = "store_unreachable"
		}
		status := resp.StatusCode
		if status >= 500 {
			status = http.StatusBadGateway
		}
		e := problem(status, code, "the plugin store refused it")
		for k, v := range ans {
			if k != "error" {
				e = e.With(k, v)
			}
		}
		httperror.Write(w, r, e)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, ans)
}

func readSmall(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (h *Admin) rate(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Stars int    `json:"stars"`
		Text  string `json:"text"`
	}
	if err := readSmall(r, &b); err != nil || b.Stars < 1 || b.Stars > 5 {
		httperror.Write(w, r, problem(http.StatusBadRequest, "field_invalid", "stars is 1 to 5"))
		return
	}
	h.toStore(w, r, "/ratings", map[string]any{"stars": b.Stars, "text": b.Text}, true)
}

func (h *Admin) reportPlugin(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Reason  string `json:"reason"`
		Details string `json:"details"`
	}
	if err := readSmall(r, &b); err != nil || b.Reason == "" {
		httperror.Write(w, r, problem(http.StatusBadRequest, "field_invalid", "a reason"))
		return
	}
	h.toStore(w, r, "/reports", map[string]any{"reason": b.Reason, "details": b.Details}, false)
}
