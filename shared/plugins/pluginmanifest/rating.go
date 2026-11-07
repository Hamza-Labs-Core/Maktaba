package pluginmanifest

import "strings"

// Rating is a plugin's rating, 1 (worst) to 6 (story 54.08 §2): advice shown on install
// and on the Plugins page, the same function in the API and the store (54.16), tested on
// one table (testdata/ratings.json). It starts at 6; each medium capability asked
// (required or optional) costs 1 and each high one 2; no store review (review level
// none, developer or development) costs 1; asking to read another plugin's data
// (plugin-data.read:<id>) costs 1 more. It is never below 1.
func Rating(capabilities []string, reviewLevel string) int {
	r := 6
	seen := map[string]bool{}
	for _, name := range capabilities {
		if seen[name] {
			continue
		}
		seen[name] = true
		c, ok := LookupCapability(name)
		if !ok {
			continue
		}
		switch c.Risk {
		case "medium":
			r--
		case "high":
			r -= 2
		}
		if strings.HasPrefix(name, "plugin-data.read:") {
			r--
		}
	}
	if reviewLevel != "store" && reviewLevel != "automated" && reviewLevel != "reviewed" {
		r--
	}
	if r < 1 {
		r = 1
	}
	return r
}
