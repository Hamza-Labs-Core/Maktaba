package httperror

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
)

// The problem-type registry (Epic 60 story 60.06): every type the API sends is an entry of
// shared/api/problems.json, which the site turns into one page per type. The package embeds
// a copy (registry_gen.json, refreshed by `go generate`; TestEmbeddedRegistryIsCurrent fails
// when it differs from the shared file).
//
//go:generate cp ../../../shared/api/problems.json registry_gen.json
//go:embed registry_gen.json
var registryJSON []byte

// Base is the namespace of every problem type the API sends.
const Base = "https://maktaba.tv/problems/"

// Text is one text of an entry in both languages.
type Text struct {
	En string `json:"en"`
	Ar string `json:"ar"`
}

// Entry is one problem type of the registry.
type Entry struct {
	Slug     string   `json:"slug"`
	Status   int      `json:"status"`
	Area     string   `json:"area"`
	Audience string   `json:"audience"`
	Codes    []string `json:"codes"`
	Title    Text     `json:"title"`
	Means    Text     `json:"means"`
	Do       Text     `json:"do"`
	Help     *string  `json:"help"`
	Install  *string  `json:"install"`
}

type registryFile struct {
	Base     string  `json:"base"`
	Problems []Entry `json:"problems"`
}

var (
	registry = mustLoadRegistry(registryJSON)
	bySlug   = indexSlugs(registry.Problems)
	byCode   = indexCodes(registry.Problems)
)

func mustLoadRegistry(b []byte) registryFile {
	var f registryFile
	if err := json.Unmarshal(b, &f); err != nil {
		panic("httperror: registry_gen.json: " + err.Error())
	}
	if f.Base != Base {
		panic(fmt.Sprintf("httperror: registry base %q, want %q", f.Base, Base))
	}
	return f
}

func indexSlugs(es []Entry) map[string]*Entry {
	m := make(map[string]*Entry, len(es))
	for i := range es {
		m[es[i].Slug] = &es[i]
	}
	return m
}

func indexCodes(es []Entry) map[string]string {
	m := map[string]string{}
	for _, e := range es {
		for _, c := range e.Codes {
			m[c] = e.Slug
		}
	}
	return m
}

// Type returns the problem type URI of slug. It does not check the slug: Write does, for
// every problem however its type was made.
func Type(slug string) string { return Base + slug }

// TypeForCode returns the type of the entry whose codes list code. A code no entry lists
// gives the type the code's slug would have (its underscores as hyphens), which Write then
// refuses as unregistered.
func TypeForCode(code string) string {
	if slug, ok := byCode[code]; ok {
		return Type(slug)
	}
	return Type(strings.ReplaceAll(code, "_", "-"))
}

// Registered reports whether typ is a registered type (an entry's URI).
func Registered(typ string) bool {
	slug, ok := strings.CutPrefix(typ, Base)
	if !ok {
		return false
	}
	_, ok = bySlug[slug]
	return ok
}

// Lookup returns the entry of typ, or nil.
func Lookup(typ string) *Entry {
	slug, ok := strings.CutPrefix(typ, Base)
	if !ok {
		return nil
	}
	return bySlug[slug]
}

// Entries returns every registry entry (for tests and tools; do not modify them).
func Entries() []Entry { return registry.Problems }

// testMode is true in a test binary; a test sets it to false to see production's answer.
var testMode = testing.Testing()

// UnregisteredEnv names the file a test binary appends each unregistered type it was asked
// to send to (one line each: the slug, the status, the request); the API's CI job fails when
// it is not empty.
const UnregisteredEnv = "MAKTABA_UNREGISTERED_PROBLEMS"

var recordMu sync.Mutex

// checkType makes e safe to send. A type outside Base (about:blank) passes. A registered
// type passes. An unregistered one, in a test binary, is recorded and sent as it is; in
// production it is logged and replaced by the generic type of e's status, keeping the status.
func checkType(r *http.Request, e *Error) *Error {
	slug, ours := strings.CutPrefix(e.Type, Base)
	if !ours && e.Type != "" {
		return e
	}
	if ours {
		if _, ok := bySlug[slug]; ok {
			return e
		}
	}
	if testMode {
		recordUnregistered(r, slug, e.Status)
		return e
	}
	slog.ErrorContext(r.Context(), "problem_type_unregistered",
		"slug", slug, "status", e.Status, "method", r.Method, "path", r.URL.Path,
		"event", "problem_type_unregistered")
	c := *e
	c.Type = GenericType(e.Status)
	return &c
}

func recordUnregistered(r *http.Request, slug string, status int) {
	if slug == "" {
		slug = "(empty type)"
	}
	line := fmt.Sprintf("%s\t%d\t%s %s\n", slug, status, r.Method, r.URL.Path)
	path := os.Getenv(UnregisteredEnv)
	if path == "" {
		slog.ErrorContext(r.Context(), "problem_type_unregistered",
			"slug", slug, "status", status, "path", r.URL.Path, "event", "problem_type_unregistered")
		return
	}
	recordMu.Lock()
	defer recordMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		slog.ErrorContext(r.Context(), "problem_type_unregistered: cannot record",
			"slug", slug, "file", path, "err", err, "event", "problem_type_unregistered")
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(line)
}

// GenericType is the registered type for a status with no type of its own.
func GenericType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return TypeBadRequest
	case http.StatusUnauthorized:
		return TypeUnauthorized
	case http.StatusForbidden:
		return TypeForbidden
	case http.StatusNotFound, http.StatusGone:
		return TypeNotFound
	case http.StatusMethodNotAllowed:
		return TypeMethodNotAllowed
	case http.StatusConflict, http.StatusPreconditionFailed, http.StatusLocked:
		return TypeConflict
	case http.StatusRequestEntityTooLarge:
		return TypeBodyTooLarge
	case http.StatusUnsupportedMediaType:
		return TypeUnsupportedMediaType
	case http.StatusUnprocessableEntity:
		return TypeValidation
	case http.StatusTooManyRequests:
		return TypeRateLimited
	case http.StatusBadGateway:
		return TypeBadGateway
	case http.StatusServiceUnavailable:
		return TypeUnavailable
	}
	if status >= 500 {
		return TypeInternal
	}
	return TypeBadRequest
}
