package webui

import (
	"html"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/pagetheme"
)

// The pages the server renders itself (the placeholder, the error and
// "cannot reach" pages of the web port, Epic 35) use the shared page theme
// (shared/go/pagetheme): the system theme, dark when the browser does not
// prefer light, never a bright white page; CSS only, no script.

// PageCSS is the shared stylesheet (kept for callers of this package).
const PageCSS = pagetheme.CSS

// Page renders a themed HTML page; body is HTML the caller built (escape user text with
// html.EscapeString).
func Page(title, body string) string {
	return `<!doctype html><html lang="en"><head>` + pagetheme.Head(title, "") +
		`</head><body><main><h1>` + html.EscapeString(title) + `</h1>` + body + `</main></body></html>`
}

// WantsHTML reports whether the request is a browser page load.
func WantsHTML(r *http.Request) bool { return pagetheme.WantsHTML(r) }

// Error answers status with a themed page for a browser page load, plain text otherwise.
func Error(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	if WantsHTML(r) {
		pagetheme.Write(w, pagetheme.Page{
			Status:       status,
			Title:        title + " · Maktaba",
			Heading:      title,
			Message:      detail,
			RetrySeconds: retryFor(status),
			Links:        []pagetheme.Link{{Href: "/", Text: "Back to Maktaba"}},
		})
		return
	}
	http.Error(w, title+": "+detail, status)
}

// retryFor: a gateway error (something is starting or out of reach) reloads by itself.
func retryFor(status int) int {
	if status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout {
		return 10
	}
	return 0
}
