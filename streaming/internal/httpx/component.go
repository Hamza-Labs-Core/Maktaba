package httpx

import (
	"encoding/json"
	"net/http"
)

// CodeComponentOff is the problem code of a route whose component is off (Epic 51,
// story 51.05): the same code the API answers.
const CodeComponentOff = "component_off"

// ComponentProblem is the 409 a route of an off component answers.
type ComponentProblem struct {
	Problem
	Code      string `json:"code"`
	Component string `json:"component"`
}

// WriteComponentOff answers 409 component_off for component id.
func WriteComponentOff(w http.ResponseWriter, id string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set(ProblemHeader, CodeComponentOff)
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(ComponentProblem{
		Problem: Problem{Type: "https://maktaba.invalid/problems/" + CodeComponentOff, Title: "component off",
			Status: http.StatusConflict, Detail: id + " is turned off on this home"},
		Code: CodeComponentOff, Component: id,
	})
}

// RequireComponent answers 409 component_off while on(id) is false (the component
// runner's gate). A nil on lets every request through.
func RequireComponent(on func(string) bool, id string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if on == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !on(id) {
				WriteComponentOff(w, id)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
