package plugins

// Linking this home to a plugin store account (story 54.15 §5), admin only:
//
//	GET    /api/plugins/store-account        linked?, the store's home id, a pending code
//	POST   /api/plugins/store-account/link   starts: a code for /link (the home store key is made once)
//	POST   /api/plugins/store-account/poll   asks the store once whether the admin confirmed
//	DELETE /api/plugins/store-account        unlinks (a request signed with the home store key)

import (
	"errors"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/storelink"
)

func (h *Admin) storeFail(w http.ResponseWriter, r *http.Request, err error) {
	var e *storelink.Error
	if errors.As(err, &e) {
		httperror.Write(w, r, problem(e.Status, e.Code, e.Msg))
		return
	}
	h.fail(w, r, err)
}

func (h *Admin) store(w http.ResponseWriter, r *http.Request) bool {
	if h.Store == nil {
		httperror.Write(w, r, problem(http.StatusServiceUnavailable, "store_unavailable", "this home has no secrets store"))
		return false
	}
	return true
}

func (h *Admin) storeStatus(w http.ResponseWriter, r *http.Request) {
	if !h.store(w, r) {
		return
	}
	st, err := h.Store.Status(r.Context())
	if err != nil {
		h.storeFail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, st)
}

func (h *Admin) storeLink(w http.ResponseWriter, r *http.Request) {
	if !h.store(w, r) {
		return
	}
	p, err := h.Store.Start(r.Context())
	if err != nil {
		h.storeFail(w, r, err)
		return
	}
	h.log().InfoContext(r.Context(), "plugins: store link started", "event", "plugin_store_link_started", "by", h.actor(r).By)
	common.WriteJSON(w, r, http.StatusOK, p)
}

func (h *Admin) storePoll(w http.ResponseWriter, r *http.Request) {
	if !h.store(w, r) {
		return
	}
	st, err := h.Store.Poll(r.Context())
	if err != nil {
		h.storeFail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, st)
}

func (h *Admin) storeUnlink(w http.ResponseWriter, r *http.Request) {
	if !h.store(w, r) {
		return
	}
	if err := h.Store.Unlink(r.Context()); err != nil {
		h.storeFail(w, r, err)
		return
	}
	h.log().InfoContext(r.Context(), "plugins: store unlinked", "event", "plugin_store_unlinked", "by", h.actor(r).By)
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"linked": false})
}
