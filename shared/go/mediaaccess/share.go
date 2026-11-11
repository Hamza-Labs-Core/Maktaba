package mediaaccess

// Epic 59 story 59.13: a NAS as the home. Its libraries are its own folders
// (/volume1/Movies on DSM, mounted in the container at the same path), and a node opens
// the same files over SMB (/Volumes/Movies on a Mac). These rules give each folder the
// share it is; the pipeline has the same rules (sharepath/nashome.py) and both read
// shared/testdata/sharepath/nas_local_roots.json. Setup's share step proposes them, the
// API's home-shares routes show them (api/internal/sharepath).

import (
	"regexp"
	"strings"
)

var (
	dsmRE      = regexp.MustCompile(`^(/volume(?:USB)?\d+/([^/]+))(?:/|$)`)
	unraidRE   = regexp.MustCompile(`^(/mnt/user0?/([^/]+))(?:/|$)`)
	qnapRE     = regexp.MustCompile(`^(/share/(?:(?:CE_)?CACHEDEV\d+_DATA/)?([^/]+))(?:/|$)`)
	qnapPoolRE = regexp.MustCompile(`^(?:CE_)?CACHEDEV\d+_DATA$`)
	catchAllRE = regexp.MustCompile(`^/media(?:/|$)`)
	hostRoots  = map[string]*regexp.Regexp{
		"dsm":     regexp.MustCompile(`^/volume(?:USB)?\d+(?:/|$)`),
		"truenas": regexp.MustCompile(`^/mnt/`),
		"unraid":  regexp.MustCompile(`^/mnt/`),
		"qts":     regexp.MustCompile(`^/share/`),
		"omv":     regexp.MustCompile(`^/srv/`),
	}
)

func cleanLocal(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if t := strings.TrimRight(p, "/"); t != "" {
		return t
	}
	return p
}

// RuleShare is the share a NAS's own folder is on by the platform's layout alone, and
// the share's folder: DSM /volume<N>/<share>, Unraid /mnt/user/<share>, QNAP
// /share/<share>. ok is false when the platform has no rule (TrueNAS, whose share
// names are not in the path; OMV; anything else) or the path is on no share.
func RuleShare(platform, path string) (share, root string, ok bool) {
	p := cleanLocal(path)
	var m []string
	switch platform {
	case "dsm":
		m = dsmRE.FindStringSubmatch(p)
	case "unraid":
		m = unraidRE.FindStringSubmatch(p)
	case "qts":
		m = qnapRE.FindStringSubmatch(p)
		if m != nil && qnapPoolRE.MatchString(m[2]) {
			m = nil
		}
	}
	if m == nil || strings.HasPrefix(m[2], "@") || strings.HasPrefix(m[2], "#") || strings.HasPrefix(m[2], ".") {
		return "", "", false
	}
	return m[2], m[1], true
}

// IsHostPath reports whether path is where the NAS itself keeps a share; false for a
// mount at another path (the old compose file's /media catch-all), which nodes match by
// the share name the household gave.
func IsHostPath(platform, path string) bool {
	p := cleanLocal(path)
	if catchAllRE.MatchString(p) {
		return false
	}
	re, ok := hostRoots[platform]
	return !ok || re.MatchString(p)
}
