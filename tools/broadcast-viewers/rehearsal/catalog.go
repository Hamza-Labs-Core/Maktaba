package rehearsal

import "strings"

// Scenario is one of story 50.29 §2's scenarios: the story whose criteria it holds, the sets that
// run it (the pull request's short set, the nightly full one), and whether it has a body yet in
// scenarios_test.go (TestBroadcast checks Built against its bodies).
type Scenario struct {
	Name    string
	Story   string
	PR      bool
	Nightly bool
	Built   bool
}

// Scenarios is §2's table, plus the fan-out's self-test (T03). A scenario with no body is listed
// in expected-failures.txt until its story turns it on.
var Scenarios = []Scenario{
	{"broadcast-fanout", "50.21", true, true, true},
	{"broadcast-fanout-planted", "50.29 T03", true, true, true},
	{"broadcast-home", "50.22", true, true, true},
	{"broadcast-home-offline-24h", "50.21", false, true, true},
	{"broadcast-plans", "50.20", true, true, false},
	{"broadcast-publishable", "50.23", true, true, false},
	{"broadcast-access", "50.26", true, true, false},
	{"broadcast-kill", "50.25", true, true, true},
	{"broadcast-takedown", "50.24", true, true, false},
	{"broadcast-hetzner-down", "14.8", false, true, false},
	{"broadcast-edge-deploy", "50.21", true, true, true},
	{"broadcast-observe", "50.28", true, true, false},
}

// InSet is the scenarios of a set ("pr" or "nightly") that have (built) or lack (!built) a body:
// the CI matrix is the built ones, each a job with the stack; the others are reported in the
// verdict job without one.
func InSet(set string, built bool) []string {
	var out []string
	for _, s := range Scenarios {
		in := s.Nightly
		if strings.EqualFold(set, "pr") {
			in = s.PR
		}
		if in && s.Built == built {
			out = append(out, s.Name)
		}
	}
	return out
}
