package middleware

import (
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
)

// CodeComponentOff is the problem code of a route whose component is off (Epic 51).
const CodeComponentOff = "component_off"

// RequireComponent answers 409 component_off, with the component's id, while component
// id is off on this home (the runner's gate, read without a lock). Put it after the
// route's role check, so nobody learns more than their role allows. A nil runner (no
// database, tests) lets every request through. It panics on an id the catalog does not
// know.
func RequireComponent(r *runner.Runner, id string) func(http.Handler) http.Handler {
	if _, ok := components.Default().Get(id); !ok {
		panic("middleware: RequireComponent: " + id + " is not in the component catalog")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !r.On(id) {
				httperror.Write(w, req, ComponentOff(id))
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// ComponentOff is the 409 a route of an off component answers.
func ComponentOff(id string) *httperror.Error {
	return httperror.Conflict("", id+" is turned off on this home").
		With("code", CodeComponentOff).With("component", id)
}
