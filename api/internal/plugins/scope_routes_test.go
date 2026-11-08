package plugins

import (
	"sort"
	"testing"
)

// routeScopes says, for every plugin API route, how the ids it is given are kept to the
// job (README §15.12). A new route fails TestEveryRouteSaysHowItsIdsAreScoped until it is
// listed here, with its check, and reviewed.
var routeScopes = map[string]string{
	// writes
	"write/set-field":    "writeScope: the video is the video job's, or of a granted library and, for a job of one library, that one",
	"write/add-tags":     "writeScope",
	"write/set-chapters": "writeScope",
	"write/add-subtitle": "writeScope",
	"write/set-artwork":  "writeScope (a video subject only)",
	"suggest/add":        "subjectScope(write): a video, library, series or season of the job's scope; other kinds refused",
	"trusted/stmt":       "statements.Executor: every check the statement's header lists (resolve.go)",
	"trusted/tx":         "an open transaction of the invocation's own only; statements in it are checked as above",
	"data/put":           "the plugin's own namespace (plugin_id from the token): no other plugin's or library's row",
	"data/delete":        "the plugin's own namespace",
	"notify/admin":       "a notice from the plugin's own strings: no id",
	"log/write":          "no id: the job's own log",
	"events/emit":        "subjectScope(read), and the event's declared subject kind",
	"queue/request":      "subjectScope(read), the stage's may_request and the lineage's loop check",
	"trusted/service":    "an allow-listed service call: no id of a row",
	"compute/embed":      "no id: a model call",
	"compute/generate":   "no id: a model call",
	// reads (ids checked by readScope: a granted library)
	"data/get":            "the plugin's own namespace",
	"data/list":           "the plugin's own namespace",
	"library/video":       "readScope",
	"library/series":      "a granted library",
	"library/list":        "a granted library",
	"transcript/segments": "readScope",
	"transcript/words":    "readScope",
	"media/open":          "writeScope: a media read acts on the job's subject",
	"media/read":          "a handle the invocation opened",
	"media/size":          "a handle the invocation opened",
	"media/close":         "a handle the invocation opened",
	"media/frame":         "writeScope",
	"media/audio":         "writeScope",
	"media/info":          "readScope",
	"exports/read":        "another plugin's exported data, by the grant",
	"job/grants":          "the token's own",
	"job/settings":        "the plugin's own settings",
}

func TestEveryRouteSaysHowItsIdsAreScoped(t *testing.T) {
	s := &Server{}
	s.init()
	var missing []string
	for r := range s.routes {
		if routeScopes[r] == "" {
			missing = append(missing, r)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("routes with no scope rule in routeScopes: %v", missing)
	}
	for r := range routeScopes {
		if _, ok := s.routes[r]; !ok {
			t.Errorf("routeScopes lists %s, which is no route", r)
		}
	}
}
