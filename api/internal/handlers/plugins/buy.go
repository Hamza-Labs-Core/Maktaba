package plugins

// Buying a paid plugin (Epic 54 story 54.19 §5):
//
//	POST /api/plugins/store-account/buy {"plugin_id", "lang"}
//
// asks the store, signed with this home's store key, for a one-time purchase intent, and
// answers its buy page and the intent: the admin's browser posts the intent there, where the
// buyer signs in to the store account this home is linked to and pays on the provider's page.
// From then on the home checks for the purchase every 15 seconds for 15 minutes. The home
// sends only the plugin, the language and its own signature: never its address.

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

var pluginIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,127}$`)

// BuyAnswer is the store's purchase intent, passed to the admin's browser.
type BuyAnswer struct {
	BuyURL    string `json:"buy_url"`
	Intent    string `json:"intent"`
	Product   string `json:"product"`
	ExpiresAt string `json:"expires_at"`
}

func (h *Admin) buy(w http.ResponseWriter, r *http.Request) {
	if !h.store(w, r) {
		return
	}
	var in struct {
		PluginID string `json:"plugin_id"`
		Lang     string `json:"lang"`
	}
	if err := readSmall(r, &in); err != nil || !pluginIDRe.MatchString(in.PluginID) {
		httperror.Write(w, r, problem(http.StatusBadRequest, "field_invalid", "a plugin id"))
		return
	}
	lang := "en"
	if in.Lang == "ar" {
		lang = "ar"
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
	body, _ := json.Marshal(map[string]string{"plugin_id": in.PluginID, "lang": lang})
	resp, raw, err := h.Store.Signed(r.Context(), http.MethodPost, "/v1/homes/"+st.HomeID+"/purchase-intents", body)
	if err != nil {
		h.storeFail(w, r, err)
		return
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = "store_unreachable"
		}
		status := resp.StatusCode
		if status >= 500 {
			status = http.StatusBadGateway
		}
		httperror.Write(w, r, problem(status, e.Error, "the plugin store refused it"))
		return
	}
	var ans BuyAnswer
	// the browser goes only to the store's own buy page for this plugin
	want := h.Store.Base() + "/buy/" + in.PluginID
	if err := json.Unmarshal(raw, &ans); err != nil || ans.BuyURL != want || ans.Intent == "" || strings.ContainsAny(ans.Intent, "\"<>& ") {
		httperror.Write(w, r, problem(http.StatusBadGateway, "store_unreachable", "the plugin store's answer was not a purchase intent"))
		return
	}
	// the purchase may land within minutes: look for it every 15 s for 15 minutes
	if h.Entitlements != nil {
		h.Entitlements.Burst()
	}
	if h.Logger != nil {
		h.Logger.Info("plugin buy started", "plugin", in.PluginID, "product", ans.Product)
	}
	common.WriteJSON(w, r, http.StatusOK, ans)
}
