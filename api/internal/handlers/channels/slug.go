package channels

import (
	"strings"
	"unicode"
)

// Slugify derives a stable, URL/guide-safe token from a channel name
// (D4). XMLTV `<channel id>` and M3U `tvg-id` bind to this slug, so it is
// generated once at create and does NOT change on rename — a drifting
// slug would break the external guide↔stream mapping in Plex/Jellyfin.
//
// Rules: lower-case, ASCII alphanumerics kept, every other run collapsed
// to a single hyphen, leading/trailing hyphens trimmed. A name with no
// usable characters (e.g. all punctuation or non-Latin script) yields
// "channel" so the slug is never empty; the caller suffixes a collision
// counter to keep it unique within scope.
func Slugify(name string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "channel"
	}
	return s
}

// SlugWithSuffix appends a numeric collision suffix (n>=2) to a base
// slug: SlugWithSuffix("kids", 2) == "kids-2". n<=1 returns the base
// unchanged so the first occurrence keeps the clean slug.
func SlugWithSuffix(base string, n int) string {
	if n <= 1 {
		return base
	}
	return base + "-" + itoa(n)
}
