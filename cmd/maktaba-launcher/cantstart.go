package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// The cut-off for the server itself (Epic 53 story 53.05 §3): CantStartMax starts of
// serve within CantStartWindow that each exit before it was up StableAfter (it never got
// ready) stop the launcher from starting it again. It writes start-failed.json in its
// updates folder (the Mac app reads it and shows its own buttons) and serves a minimal
// "can't start" page itself on this machine's web port (loopback only: its buttons act
// on the server), until a choice is made there or by the app: try again, start in safe
// mode, or go back to the version before. A boot loop of a new, pending version keeps
// its automatic rollback; this is for the version that runs.

// The cut-off's numbers.
const (
	CantStartMax    = 3
	CantStartWindow = 2 * time.Minute
	// cantStartLines is how many of the server's last log lines the record keeps.
	cantStartLines = 50
)

// StartFailedFile is the record, and StartChoiceFile the choice the app (or the page)
// leaves for the launcher: "retry", "safe" or "rollback".
const (
	StartFailedFile = "start-failed.json"
	StartChoiceFile = "start-choice"
)

// StartFailed is start-failed.json.
type StartFailed struct {
	At        time.Time   `json:"at"`
	Version   string      `json:"version"`
	Exits     []StartExit `json:"exits"`
	LastLines []string    `json:"last_lines"`
	// Rollback is the version the launcher can go back to ("" when none is kept).
	Rollback string `json:"rollback,omitempty"`
}

// StartExit is one early exit.
type StartExit struct {
	Code int       `json:"code"`
	At   time.Time `json:"at"`
}

// earlyExit notes an exit of the running version's serve and reports whether the cut-off
// is reached.
func (ln *launcher) earlyExit(res runResult) bool {
	if res.ran >= ln.cfg.StableAfter {
		ln.early = nil
		return false
	}
	now := ln.cfg.Now()
	ln.early = append(ln.early, StartExit{Code: res.code, At: now})
	kept := ln.early[:0]
	for _, e := range ln.early {
		if now.Sub(e.At) <= CantStartWindow {
			kept = append(kept, e)
		}
	}
	ln.early = kept
	limit := ln.cfg.CantStartMax
	if limit == 0 {
		limit = CantStartMax
	}
	return limit > 0 && len(ln.early) >= limit
}

// cantStart records the cut-off and waits for a choice (or a stop: false). It answers
// what to do: the server's next start in safe mode, or a rollback made.
func (ln *launcher) cantStart(ctx context.Context, ver string) (choice string, ok bool) {
	rec := StartFailed{At: ln.cfg.Now().UTC(), Version: ver, Exits: append([]StartExit(nil), ln.early...),
		LastLines: ln.lastLines(), Rollback: ln.rollbackTo()}
	dir := ln.l.UpdatesDir()
	_ = os.MkdirAll(dir, 0o700)
	// a choice left from before is no answer to this record: gone before the record is
	// there for the app to read
	_ = os.Remove(filepath.Join(dir, StartChoiceFile))
	if b, err := json.MarshalIndent(rec, "", "  "); err == nil {
		_ = layout.WriteFileAtomic(filepath.Join(dir, StartFailedFile), b, 0o600)
	}
	ln.log.Event("launcher_cant_start", ver, map[string]any{"exits": len(rec.Exits)})
	stop := ln.servePage(rec)
	defer stop()
	t := time.NewTicker(ln.cfg.Poll)
	defer t.Stop()
	for {
		if c := ln.takeChoice(); c != "" {
			_ = os.Remove(filepath.Join(dir, StartFailedFile))
			ln.early = nil
			ln.log.Event("launcher_start_choice", ver, map[string]any{"choice": c})
			return c, true
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-t.C:
		}
	}
}

// takeChoice reads the choice file and removes it once it holds one of the three ("" when
// none). A file that holds nothing yet is left: the app may be writing it now, and removing
// it then would lose the choice.
func (ln *launcher) takeChoice() string {
	p := filepath.Join(ln.l.UpdatesDir(), StartChoiceFile)
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	switch c := strings.TrimSpace(string(b)); c {
	case "retry", "safe", "rollback":
		_ = os.Remove(p)
		return c
	}
	return ""
}

// rollbackTo is the version a rollback would run ("" when none is kept).
func (ln *launcher) rollbackTo() string {
	if ln.cfg.Bundle != "" {
		if g := ln.bundleGuard(); g != nil && g.PreviousPayload != "" {
			return payloadVersion(g.PreviousPayload)
		}
		return ""
	}
	prev, err := ln.l.Previous()
	if err != nil {
		return ""
	}
	return prev
}

// lastLines are the server log's last lines, scrubbed with the shared list.
func (ln *launcher) lastLines() []string {
	path := ln.cfg.LogPath
	if path == "" {
		if f, ok := ln.cfg.Stderr.(*os.File); ok {
			if fi, err := f.Stat(); err == nil && fi.Mode().IsRegular() {
				path = f.Name()
			}
		}
	}
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	off := fi.Size() - 256<<10
	if off < 0 {
		off = 0
	}
	b := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // a cut first line
	}
	if len(lines) > cantStartLines {
		lines = lines[len(lines)-cantStartLines:]
	}
	for i, l := range lines {
		if len(l) > 2000 {
			l = l[:2000] + "…"
		}
		lines[i] = mlog.Scrub(l)
	}
	return lines
}

// servePage serves the can't-start page on the loopback web port until stop.
func (ln *launcher) servePage(rec StartFailed) (stop func()) {
	addr := ln.cfg.PageAddr
	if addr == "" {
		addr = "127.0.0.1:8088"
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		ln.log.Event("launcher_cant_start_page", rec.Version, map[string]any{"err": err.Error()})
		return func() {}
	}
	token := randHex()
	srv := &http.Server{Handler: ln.pageHandler(rec, token), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(lis) }()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// pageHandler is the page and its buttons (a form post with the page's token: no other
// site can press them).
func (ln *launcher) pageHandler(rec StartFailed, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := pageLang(r.Header.Get("Accept-Language"))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/launcher/choice":
			if r.FormValue("token") != token {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			c := r.FormValue("choice")
			if c != "retry" && c != "safe" && (c != "rollback" || rec.Rollback == "") {
				http.Error(w, "bad choice", http.StatusBadRequest)
				return
			}
			_ = layout.WriteFileAtomic(filepath.Join(ln.l.UpdatesDir(), StartChoiceFile), []byte(c+"\n"), 0o600)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, cantStartHTML(lang, rec, "", pageWords[lang]["starting"]))
		case r.Method == http.MethodGet && r.URL.Path == "/launcher/diagnostics":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", `attachment; filename="maktaba-start-failed.json"`)
			_ = json.NewEncoder(w).Encode(rec)
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, cantStartHTML(lang, rec, token, ""))
		}
	})
}

// pageWords are the page's strings (story 53.05 §4), English and Arabic.
var pageWords = map[string]map[string]string{
	"en": {"title": "Maktaba can't start", "body": "It stopped {count} times in a row. Here is what it said last.",
		"retry": "Try again", "safe": "Start in safe mode", "rollback": "Go back to version {version}",
		"diagnostics": "Download diagnostics", "starting": "Starting Maktaba again…"},
	"ar": {"title": "تعذّر تشغيل مكتبة", "body": "توقف {count} مرات متتالية. هذا آخر ما سجّله.",
		"retry": "حاول مجددًا", "safe": "التشغيل في الوضع الآمن", "rollback": "الرجوع إلى الإصدار {version}",
		"diagnostics": "تنزيل ملف التشخيص", "starting": "جارٍ تشغيل مكتبة من جديد…"},
}

func pageLang(accept string) string {
	for _, part := range strings.Split(accept, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		if strings.HasPrefix(tag, "ar") {
			return "ar"
		}
		if strings.HasPrefix(tag, "en") {
			return "en"
		}
	}
	return "en"
}

func cantStartHTML(lang string, rec StartFailed, token, note string) string {
	w := pageWords[lang]
	e := html.EscapeString
	dir := "ltr"
	if lang == "ar" {
		dir = "rtl"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><html lang="%s" dir="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`+
		`<meta name="color-scheme" content="dark light"><title>%s</title><style>body{font-family:system-ui,sans-serif;background:#09090b;color:#fafafa;`+
		`max-width:860px;margin:auto;padding:24px}pre{white-space:pre-wrap;background:#111113;padding:8px;border-radius:6px;font-size:.8rem}`+
		`button{font:inherit;padding:6px 12px;margin:4px;border-radius:6px}a{color:#93c5fd}</style></head><body><h1>%s</h1>`,
		lang, dir, e(w["title"]), e(w["title"]))
	if note != "" {
		fmt.Fprintf(&b, "<p>%s</p></body></html>", e(note))
		return b.String()
	}
	fmt.Fprintf(&b, "<p>%s</p><pre>%s</pre>", e(strings.ReplaceAll(w["body"], "{count}", fmt.Sprint(len(rec.Exits)))),
		e(strings.Join(rec.LastLines, "\n")))
	button := func(choice, label string) {
		fmt.Fprintf(&b, `<form method="post" action="/launcher/choice" style="display:inline"><input type="hidden" name="token" value="%s">`+
			`<input type="hidden" name="choice" value="%s"><button>%s</button></form>`, e(token), e(choice), e(label))
	}
	button("retry", w["retry"])
	button("safe", w["safe"])
	if rec.Rollback != "" {
		button("rollback", strings.ReplaceAll(w["rollback"], "{version}", rec.Rollback))
	}
	fmt.Fprintf(&b, ` <a href="/launcher/diagnostics">%s</a></body></html>`, e(w["diagnostics"]))
	return b.String()
}
