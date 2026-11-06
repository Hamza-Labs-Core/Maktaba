package log

import "log/slog"

// Err is a failure's `err` object (story 53.01 §1): its kind (unreachable,
// timeout, db, ...), a stable code slug and the scrubbed message. Lines that
// predate it carry `err` as a plain string; the schema reads both.
func Err(err error, kind, code string) slog.Attr {
	msg := ""
	if err != nil {
		msg = Scrub(err.Error())
	}
	attrs := []any{slog.String("msg", msg)}
	if kind != "" {
		attrs = append(attrs, slog.String("kind", kind))
	}
	if code != "" {
		attrs = append(attrs, slog.String("code", code))
	}
	return slog.Group("err", attrs...)
}
