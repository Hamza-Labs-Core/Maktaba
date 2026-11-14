// Package compat is the compatibility contract between a home and its apps (Epic 58 story
// 58.01).
//
// The home's API has one integer, its API level (Level). It rises by one each time a
// client-visible contract changes in a way an older app would get wrong: a field's meaning
// changes, a route goes, a stream format changes. Adding fields does not raise it. Each level
// and what it changed is in docs/api/levels.md, and shared/api/levels/<n>.yaml is the
// response surface level n promised (the levelcheck test in this package fails an
// incompatible change to shared/api/openapi.yaml until the level is raised).
//
// GET /api/discovery/home publishes Level, Oldest (the oldest level this home still answers
// correctly) and the client floor (the oldest build of each app/platform this home serves).
// Each app carries the oldest home level it can use and the level it was built for, and
// Decide says what it does at a home; shared/compat/vectors.json holds the cases every app's
// copy of Decide must agree on.
package compat

import (
	"strconv"
	"strings"
)

// Level is this home's API level.
const Level = 1

// Oldest is the oldest API level this home still answers correctly. A home keeps serving
// every level released in the last 12 months: Oldest never rises faster than that, and
// raising it is a release note line and a docs/api/levels.md entry.
const Oldest = 1

// Floor is the oldest build of each app this home serves, keyed "<app>/<platform>" as the
// X-Maktaba-Client header names them (e.g. "tv-web/webos": "1.4.0").
type Floor map[string]string

// ReleaseFloor is the client floor this release ships. It is empty: a home release raises an
// entry only when it must refuse an old build, for example after a security fix, and says so
// in docs/api/levels.md.
var ReleaseFloor = Floor{}

// Key is the floor key of an app on a platform.
func Key(app, platform string) string { return app + "/" + platform }

// Below reports whether version is below the floor for app/platform, and that floor. A
// version that cannot be read, or an app/platform the floor does not name, is never below.
func (f Floor) Below(app, platform, version string) (floor string, below bool) {
	floor, ok := f[Key(app, platform)]
	if !ok {
		return "", false
	}
	c, ok := CompareVersions(version, floor)
	return floor, ok && c < 0
}

// CompareVersions compares two dotted versions ("1.4.0", "0.3.140", "1.3.9-beta"): -1, 0 or
// 1, and false when either has no number to read. Each part counts by its leading digits; a
// missing part is 0, so "1.4" equals "1.4.0".
func CompareVersions(a, b string) (int, bool) {
	pa, oka := versionParts(a)
	pb, okb := versionParts(b)
	if !oka || !okb {
		return 0, false
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
	}
	return 0, true
}

// versionParts reads the leading digits of each dot-separated part, up to the first part
// with none (a pre-release or build suffix ends the number).
func versionParts(v string) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	var out []int
	for _, p := range strings.Split(v, ".") {
		end := 0
		for end < len(p) && p[end] >= '0' && p[end] <= '9' {
			end++
		}
		if end == 0 {
			break
		}
		n, err := strconv.Atoi(p[:end])
		if err != nil {
			return nil, false
		}
		out = append(out, n)
		if end < len(p) {
			break
		}
	}
	return out, len(out) > 0
}

// Verdict is what an app does at a home.
type Verdict string

const (
	// Carry is carry on: features the home lacks are hidden through GET /api/capabilities.
	Carry Verdict = "ok"
	// UpdateHome is the Update the home page: the home is older than the app can use.
	UpdateHome Verdict = "update_home"
	// UpdateApp is the Update this app page: the home no longer serves this build.
	UpdateApp Verdict = "update_app"
)

// Home is what an app read from GET /api/discovery/home. A home older than this story sends
// none of these fields: both levels read 0, and the floor is empty.
type Home struct {
	APILevel       int   `json:"api_level"`
	APILevelOldest int   `json:"api_level_oldest"`
	ClientFloor    Floor `json:"client_floor"`
}

// App is what an app knows about itself.
type App struct {
	Name             string `json:"name"`     // the X-Maktaba-Client app
	Platform         string `json:"platform"` // the X-Maktaba-Client platform
	Version          string `json:"version"`
	MinAPILevel      int    `json:"min_api_level"`       // the oldest home level it can use
	BuiltForAPILevel int    `json:"built_for_api_level"` // the level it was built for
}

// Decide is what app does at home: Update the home when the home is below the app's oldest
// level; Update this app when the app was built for a level the home no longer answers, or
// its version is below the home's floor; else carry on.
func Decide(home Home, app App) Verdict {
	if home.APILevel < app.MinAPILevel {
		return UpdateHome
	}
	if app.BuiltForAPILevel < home.APILevelOldest {
		return UpdateApp
	}
	if _, below := home.ClientFloor.Below(app.Name, app.Platform, app.Version); below {
		return UpdateApp
	}
	return Carry
}
