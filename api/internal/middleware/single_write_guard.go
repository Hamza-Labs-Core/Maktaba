package middleware

import (
	"log/slog"
	"net/http"
)

// guardedWriter wraps an http.ResponseWriter and drops second
// WriteHeader / Write calls. Story 7.1 EC: a handler that calls
// httperror.Write twice (e.g. by returning *and* writing in defer)
// has the second write swallowed and a warn log emitted, so the
// response remains coherent.
type guardedWriter struct {
	http.ResponseWriter
	written bool
	ctx     *slog.Logger
	status  int
}

func (g *guardedWriter) WriteHeader(code int) {
	if g.written {
		g.ctx.Warn("double_write_header", "second_status", code, "first_status", g.status)
		return
	}
	g.written = true
	g.status = code
	g.ResponseWriter.WriteHeader(code)
}

func (g *guardedWriter) Write(b []byte) (int, error) {
	if !g.written {
		g.WriteHeader(http.StatusOK)
	}
	return g.ResponseWriter.Write(b)
}

// Status returns the status code captured for the response. Returns 0
// if the handler wrote nothing.
func (g *guardedWriter) Status() int { return g.status }

// SingleWriteGuard wraps the response writer so a second WriteHeader
// or a WriteHeader after Write is dropped with a warn log.
func SingleWriteGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gw := &guardedWriter{ResponseWriter: w, ctx: slog.Default()}
		next.ServeHTTP(gw, r)
	})
}
