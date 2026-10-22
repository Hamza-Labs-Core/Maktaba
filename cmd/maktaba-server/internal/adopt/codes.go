package adopt

import "fmt"

// Note is one finding of a plan: a code the screens say in their own words
// (setup.move.note.<code> in English and Arabic, with params; a size is in
// bytes under a *_bytes name) and its English, for the terminal and a screen
// that does not know the code (contract-setup.md, the adopt step).
type Note struct {
	Code    string         `json:"code"`
	Params  map[string]any `json:"params,omitempty"`
	Message string         `json:"message"`
}

func (n Note) String() string { return n.Message }

func note(code, message string, kv ...any) Note {
	return Note{Code: code, Params: params(kv), Message: message}
}

// Error is a move's failure with a code the screens say in their own words
// (setup.error.<code>, with params); Err is its English, for the terminal, the
// log and the screens' Details. A code keeps its meaning and its params once
// shipped: a new reason is a new code.
type Error struct {
	Code   string
	Params map[string]any
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// coded is err with a code and its params (key, value pairs).
func coded(code string, err error, kv ...any) *Error {
	return &Error{Code: code, Params: params(kv), Err: err}
}

// codedf is coded with an English message of its own.
func codedf(code string, kv []any, format string, a ...any) *Error {
	return coded(code, fmt.Errorf(format, a...), kv...)
}

func params(kv []any) map[string]any {
	if len(kv) < 2 {
		return nil
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return m
}
