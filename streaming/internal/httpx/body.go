package httpx

import (
	"net/http"
	"strconv"
)

// WriteBody sends a whole body held in memory with its Content-Length. A body the
// server sends without one goes out in chunks once it is larger than the server's
// buffer (2 KB), and a chunked body cut short by an orderly close (the streaming
// service exiting or restarting mid-answer, a hop that stops forwarding) reads as
// whole to Apple's URL loading system: a player then decodes a truncated segment
// instead of asking for it again. With its length, a body cut short is short of it,
// and every client takes it as the error it is. Every segment, playlist and
// subtitle a player loads goes out through here or with a length set beforehand.
func WriteBody(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// AbortIfCut ends the response without completing it when the copy of its body
// failed (err is not nil): net/http then closes the connection without the last
// chunk a chunked body ends with (http.ErrAbortHandler), so a client never takes
// what was copied for the whole answer. A copy that ended well (nil) returns.
// Only for a handler that has begun its answer; it must not recover the panic.
func AbortIfCut(err error) {
	if err != nil {
		panic(http.ErrAbortHandler)
	}
}
