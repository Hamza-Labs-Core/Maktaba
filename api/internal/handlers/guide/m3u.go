package guide

import (
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// M3U implements GET /api/channels/playlist.m3u (AC5). Each #EXTINF
// carries tvg-id == slug (matching the XMLTV channel id, AC6), and is
// followed by the channel's live HLS URL.
func (h *Handler) M3U(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	chans, err := h.visibleChannels(r, p, r.URL.Query().Get("category"))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list channels"))
		return
	}
	base := baseURL(r)
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Content-Disposition", `attachment; filename="maktaba.m3u"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(buildM3U(base, chans)))
}

// buildM3U renders the playlist body. Pure so it is unit-tested without
// a request.
func buildM3U(base string, chans []ChannelMeta) string {
	var b strings.Builder
	xmltvURL := base + "/api/channels/xmltv"
	b.WriteString(`#EXTM3U url-tvg="` + xmltvURL + `" x-tvg-url="` + xmltvURL + `"` + "\n")
	for _, c := range chans {
		logo := ""
		if c.LogoPath != nil {
			logo = *c.LogoPath
		}
		b.WriteString("#EXTINF:-1")
		b.WriteString(` tvg-id="` + m3uAttr(c.Slug) + `"`)
		b.WriteString(` tvg-name="` + m3uAttr(c.Name) + `"`)
		if logo != "" {
			b.WriteString(` tvg-logo="` + m3uAttr(logo) + `"`)
		}
		b.WriteString(` tvg-chno="` + itoa(c.Number) + `"`)
		b.WriteString(` group-title="` + m3uAttr(c.Category) + `"`)
		b.WriteString("," + sanitizeName(c.Name) + "\n")
		b.WriteString(base + "/stream/channel/" + c.ID + "/manifest.m3u8\n")
	}
	return b.String()
}

// baseURL derives the public base from the inbound request (scheme +
// host), so the playlist works over a LAN IP and the relay host alike.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
		scheme = xf
	}
	host := r.Host
	if xf := r.Header.Get("X-Forwarded-Host"); xf != "" {
		host = xf
	}
	return scheme + "://" + host
}

// m3uAttr strips the quote/newline characters that would corrupt an
// EXTINF attribute value.
func m3uAttr(s string) string {
	return strings.NewReplacer(`"`, "", "\n", " ", "\r", "").Replace(s)
}

// sanitizeName strips newlines from the trailing channel title.
func sanitizeName(s string) string {
	return strings.NewReplacer("\n", " ", "\r", "", ",", " ").Replace(s)
}
