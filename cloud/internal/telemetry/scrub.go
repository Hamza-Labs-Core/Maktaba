package telemetry

import (
	"net"
	"path"
	"regexp"
	"strings"
)

// Scrub is the cloud's own pass over free text (error messages and stack
// frames). Homes and apps scrub before sending; this pass assumes they
// missed something. It removes URLs, e-mail addresses, home folders,
// the directories of absolute paths (and the whole path unless its file
// is source code), IP addresses, host names and query strings.
func Scrub(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = reURL.ReplaceAllString(s, "<url>")
	s = reEmail.ReplaceAllString(s, "<email>")
	s = reWinPath.ReplaceAllStringFunc(s, func(m string) string {
		return keepSourceFile(m[strings.LastIndexByte(m, '\\')+1:])
	})
	s = reUnixPath.ReplaceAllStringFunc(s, func(m string) string {
		lead := ""
		if m != "" && m[0] != '/' && m[0] != '~' {
			lead, m = m[:1], m[1:]
		}
		return lead + keepSourceFile(path.Base(m))
	})
	s = reIPv6.ReplaceAllStringFunc(s, func(m string) string {
		bare := strings.Trim(m, "[]")
		if i := strings.IndexByte(bare, '%'); i >= 0 {
			bare = bare[:i]
		}
		if ip := net.ParseIP(bare); ip != nil {
			return "<ip>"
		}
		return m
	})
	s = reIPv4.ReplaceAllString(s, "<ip>")
	s = reHost.ReplaceAllStringFunc(s, func(m string) string {
		if isHostName(m) {
			return "<host>"
		}
		return m
	})
	s = reQuery.ReplaceAllString(s, "")
	return strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
}

var (
	reURL   = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]{1,15}://[^\s'"<>()\[\]]+`)
	reEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	// C:\Users\x\..., \\server\share\...
	reWinPath = regexp.MustCompile(`(?i)(?:[a-z]:|\\)\\(?:[^\\\s:'"<>|]+\\)*[^\\\s:'"<>|()]*`)
	// /a/b, ~/a, at the start or after a non-word, non-path character.
	reUnixPath = regexp.MustCompile(`(?:^|[\s(\["'=,;])(?:~|/[^/\s:'"()\[\],;]+)(?:/[^/\s:'"()\[\],;]+)+/?`)
	reIPv4     = regexp.MustCompile(`\b(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}\b`)
	reIPv6     = regexp.MustCompile(`\[?[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:%[0-9A-Za-z]+)?\]?`)
	reHost     = regexp.MustCompile(`(?i)\b[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+\b`)
	reQuery    = regexp.MustCompile(`\?[^\s'"#?]*=[^\s'"]*`)
	reSpaces   = regexp.MustCompile(`\s+`)
)

// sourceExt are the file extensions kept from a path: a stack frame's
// file name is useful and never a user's file.
var sourceExt = map[string]bool{
	"go": true, "js": true, "mjs": true, "cjs": true, "ts": true, "tsx": true, "jsx": true,
	"swift": true, "kt": true, "kts": true, "java": true, "py": true, "rs": true, "c": true,
	"cc": true, "cpp": true, "h": true, "hpp": true, "m": true, "mm": true, "dart": true, "cs": true,
}

func keepSourceFile(base string) string {
	if i := strings.LastIndexByte(base, '.'); i > 0 && sourceExt[strings.ToLower(base[i+1:])] {
		return base
	}
	return "<path>"
}

// hostTLDs are top-level labels that make a dotted word a host name.
// Two-letter labels count too (country codes) unless they are a source
// extension (js, go, ts, ...).
var hostTLDs = map[string]bool{
	"com": true, "net": true, "org": true, "app": true, "dev": true, "io": true, "info": true,
	"biz": true, "edu": true, "gov": true, "mil": true, "cloud": true, "online": true, "site": true,
	"xyz": true, "tech": true, "local": true, "lan": true, "home": true, "internal": true,
	"localdomain": true, "arpa": true, "test": true, "example": true, "invalid": true,
	"localhost": true, "corp": true, "intranet": true, "private": true, "box": true, "network": true,
}

func isHostName(m string) bool {
	last := strings.ToLower(m[strings.LastIndexByte(m, '.')+1:])
	if sourceExt[last] {
		return false
	}
	if hostTLDs[last] {
		return true
	}
	if len(last) == 2 && last[0] >= 'a' && last[0] <= 'z' && last[1] >= 'a' && last[1] <= 'z' {
		return true
	}
	return false
}
