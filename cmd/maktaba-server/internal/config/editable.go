package config

// The settings an admin changes from the web (Epic 45, Settings → Storage &
// discovery): [media] path_map and roots, and the [server] addresses, public
// URLs and mdns. The supervisor's /config/settings serves and changes them;
// everything else in server.toml stays as it is.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PathPair is one [media] path_map entry: a prefix the database stores and
// this machine's folder for it.
type PathPair struct {
	DB    string `json:"db"`
	Local string `json:"local"`
}

// EditableMedia is the [media] part of Editable.
type EditableMedia struct {
	PathMap []PathPair `json:"path_map"`
	Roots   []string   `json:"roots"`
}

// EditableServer is the [server] part of Editable.
type EditableServer struct {
	MDNS         bool   `json:"mdns"`
	Listen       string `json:"listen"`
	WebListen    string `json:"web_listen"`
	StreamAddr   string `json:"stream_addr"`
	PublicURL    string `json:"public_url"`
	WebPublicURL string `json:"web_public_url"`
}

// Editable is the settings document (S in the spec).
type Editable struct {
	Media  EditableMedia  `json:"media"`
	Server EditableServer `json:"server"`
}

// EditableOf is c's settings document. Slices are never nil, so two documents
// compare equal when their values do.
func EditableOf(c Config) Editable {
	roots := append([]string{}, c.Media.Roots...)
	return Editable{
		Media: EditableMedia{PathMap: ParsePathPairs(c.Media.PathMap), Roots: roots},
		Server: EditableServer{
			MDNS:         c.Server.MDNSOn(),
			Listen:       c.Server.Listen,
			WebListen:    c.Server.WebListen,
			StreamAddr:   c.Server.StreamAddr,
			PublicURL:    c.Server.PublicURL,
			WebPublicURL: c.Server.WebPublicURL,
		},
	}
}

// ParsePathPairs reads a path_map the way the workers and the API's agent do:
// "db=local" pairs separated by ";", or a JSON object. A pair without "=" or
// with an empty side is left out.
func ParsePathPairs(text string) []PathPair {
	out := []PathPair{}
	text = strings.TrimSpace(text)
	if text == "" {
		return out
	}
	if strings.HasPrefix(text, "{") {
		var m map[string]string
		if json.Unmarshal([]byte(text), &m) != nil {
			return out
		}
		for k, v := range m {
			if k = strings.TrimSpace(k); k != "" && strings.TrimSpace(v) != "" {
				out = append(out, PathPair{DB: k, Local: strings.TrimSpace(v)})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].DB < out[j].DB })
		return out
	}
	for _, part := range strings.Split(text, ";") {
		db, local, ok := strings.Cut(part, "=")
		db, local = strings.TrimSpace(db), strings.TrimSpace(local)
		if ok && db != "" && local != "" {
			out = append(out, PathPair{DB: db, Local: local})
		}
	}
	return out
}

// FormatPathPairs is pairs in the file's form ("db=local;db2=local2").
func FormatPathPairs(pairs []PathPair) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, p.DB+"="+p.Local)
	}
	return strings.Join(parts, ";")
}

// MapToLocal maps a database path to this machine's through pairs: the longest
// matching db prefix (case-insensitive, either slash) wins; unmapped paths come
// back unchanged.
func MapToLocal(pairs []PathPair, p string) string {
	norm := func(s string) string {
		s = strings.ReplaceAll(s, `\`, "/")
		if !strings.HasSuffix(s, "/") {
			s += "/"
		}
		return s
	}
	slashed := strings.ReplaceAll(p, `\`, "/")
	low := strings.ToLower(norm(slashed))
	best, bestLen := -1, 0
	for i, pair := range pairs {
		db := strings.ToLower(norm(pair.DB))
		if strings.HasPrefix(low, db) && len(db) > bestLen {
			best, bestLen = i, len(db)
		}
	}
	if best < 0 {
		return p
	}
	rest := ""
	if bestLen <= len(slashed) {
		rest = slashed[bestLen:]
	}
	return filepath.FromSlash(norm(strings.ReplaceAll(pairs[best].Local, `\`, "/")) + rest)
}

// SettingsPatch is a PUT's body: only the keys sent change.
type SettingsPatch struct {
	Media *struct {
		PathMap *[]PathPair `json:"path_map"`
		Roots   *[]string   `json:"roots"`
	} `json:"media"`
	Server *struct {
		MDNS         *bool   `json:"mdns"`
		Listen       *string `json:"listen"`
		WebListen    *string `json:"web_listen"`
		StreamAddr   *string `json:"stream_addr"`
		PublicURL    *string `json:"public_url"`
		WebPublicURL *string `json:"web_public_url"`
	} `json:"server"`
}

// Apply puts the patch's values into s (trimmed) and names the keys whose value
// changed ("server.listen", "media.path_map", …).
func (p SettingsPatch) Apply(s *Editable) []string {
	var changed []string
	str := func(key string, from *string, to *string) {
		if from == nil {
			return
		}
		v := strings.TrimSpace(*from)
		if v != *to {
			changed = append(changed, key)
		}
		*to = v
	}
	if m := p.Media; m != nil {
		if m.PathMap != nil {
			pairs := make([]PathPair, 0, len(*m.PathMap))
			for _, pp := range *m.PathMap {
				pairs = append(pairs, PathPair{DB: strings.TrimSpace(pp.DB), Local: strings.TrimSpace(pp.Local)})
			}
			if FormatPathPairs(pairs) != FormatPathPairs(s.Media.PathMap) {
				changed = append(changed, "media.path_map")
			}
			s.Media.PathMap = pairs
		}
		if m.Roots != nil {
			roots := make([]string, 0, len(*m.Roots))
			for _, r := range *m.Roots {
				roots = append(roots, strings.TrimSpace(r))
			}
			if strings.Join(roots, "\x00") != strings.Join(s.Media.Roots, "\x00") {
				changed = append(changed, "media.roots")
			}
			s.Media.Roots = roots
		}
	}
	if sv := p.Server; sv != nil {
		if sv.MDNS != nil {
			if *sv.MDNS != s.Server.MDNS {
				changed = append(changed, "server.mdns")
			}
			s.Server.MDNS = *sv.MDNS
		}
		str("server.listen", sv.Listen, &s.Server.Listen)
		str("server.web_listen", sv.WebListen, &s.Server.WebListen)
		str("server.stream_addr", sv.StreamAddr, &s.Server.StreamAddr)
		str("server.public_url", sv.PublicURL, &s.Server.PublicURL)
		str("server.web_public_url", sv.WebPublicURL, &s.Server.WebPublicURL)
	}
	return changed
}

// Into writes s into c (mdns as "on"/"off").
func (s Editable) Into(c *Config) {
	c.Media.PathMap = FormatPathPairs(s.Media.PathMap)
	c.Media.Roots = append([]string{}, s.Media.Roots...)
	c.Server.MDNS = "off"
	if s.Server.MDNS {
		c.Server.MDNS = "on"
	}
	c.Server.Listen = s.Server.Listen
	c.Server.WebListen = s.Server.WebListen
	c.Server.StreamAddr = s.Server.StreamAddr
	c.Server.PublicURL = s.Server.PublicURL
	c.Server.WebPublicURL = s.Server.WebPublicURL
}

// FieldError names a bad value: the field as the document spells it
// ("server.listen", "media.path_map[1].local") and why.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Checks is what validation asks of the machine; the zero value uses the real one.
type Checks struct {
	// PortFree reports whether addr can be listened on now (nil: a net.Listen probe).
	PortFree func(addr string) error
}

func (c Checks) portFree(addr string) error {
	if c.PortFree != nil {
		return c.PortFree(addr)
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return l.Close()
}

// Validate checks next. An address that differs from both saved and running
// must be free: the one serve holds now is not.
func (c Checks) Validate(next, saved, running Editable) []FieldError {
	var errs []FieldError
	add := func(field, format string, a ...any) {
		errs = append(errs, FieldError{Field: field, Message: fmt.Sprintf(format, a...)})
	}

	addrs := []struct {
		field, next, saved, running string
	}{
		{"server.listen", next.Server.Listen, saved.Server.Listen, running.Server.Listen},
		{"server.web_listen", next.Server.WebListen, saved.Server.WebListen, running.Server.WebListen},
		{"server.stream_addr", next.Server.StreamAddr, saved.Server.StreamAddr, running.Server.StreamAddr},
	}
	good := make([]bool, len(addrs))
	for i, a := range addrs {
		if err := checkAddr(a.next); err != nil {
			add(a.field, "%s", err)
			continue
		}
		good[i] = true
		if a.next == a.saved || a.next == a.running {
			continue
		}
		if err := c.portFree(a.next); err != nil {
			add(a.field, "%s is in use on this machine", a.next)
		}
	}
	for i := range addrs {
		for j := 0; j < i; j++ {
			if good[i] && good[j] && sameBind(addrs[i].next, addrs[j].next) {
				add(addrs[i].field, "the same address as %s", strings.TrimPrefix(addrs[j].field, "server."))
				add(addrs[j].field, "the same address as %s", strings.TrimPrefix(addrs[i].field, "server."))
			}
		}
	}

	for _, u := range []struct{ field, v string }{
		{"server.public_url", next.Server.PublicURL},
		{"server.web_public_url", next.Server.WebPublicURL},
	} {
		if err := checkPublicURL(u.v); err != nil {
			add(u.field, "%s", err)
		}
	}

	seen := map[string]bool{}
	for i, p := range next.Media.PathMap {
		field := fmt.Sprintf("media.path_map[%d]", i)
		if strings.ContainsAny(p.DB+p.Local, ";=") {
			add(field, `a path cannot hold ";" or "="`)
			continue
		}
		if !absAnyOS(p.DB) {
			add(field+".db", "must be an absolute path")
		}
		key := strings.ToLower(strings.TrimRight(strings.ReplaceAll(p.DB, `\`, "/"), "/"))
		if seen[key] {
			add(field+".db", "%s is mapped twice", p.DB)
		}
		seen[key] = true
		if !filepath.IsAbs(p.Local) {
			add(field+".local", "must be an absolute path on this machine")
		} else if err := isDir(p.Local); err != nil {
			add(field+".local", "%s", err)
		}
	}
	for i, r := range next.Media.Roots {
		field := fmt.Sprintf("media.roots[%d]", i)
		if !absAnyOS(r) {
			add(field, "must be an absolute path")
			continue
		}
		local := MapToLocal(next.Media.PathMap, r)
		if !filepath.IsAbs(local) {
			add(field, "%s has no folder on this machine (add it to the path map)", r)
			continue
		}
		if err := isDir(local); err != nil {
			add(field, "%s", err)
		}
	}
	return errs
}

// OnlyChanged keeps the errors of the keys a write changes (and of the roots when
// the path map changed: they are found through it), so a value already saved that
// no longer checks out (a share not mounted now) does not block another change.
func OnlyChanged(errs []FieldError, changed []string) []FieldError {
	var out []FieldError
	for _, e := range errs {
		for _, k := range changed {
			if e.Field == k || strings.HasPrefix(e.Field, k+"[") || strings.HasPrefix(e.Field, k+".") ||
				(k == "media.path_map" && strings.HasPrefix(e.Field, "media.roots[")) {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// checkAddr: host:port, the port 1–65535, the host empty, an IP or localhost.
func checkAddr(a string) error {
	if a == "" {
		return fmt.Errorf("an address is needed (host:port)")
	}
	host, port, err := net.SplitHostPort(a)
	if err != nil {
		return fmt.Errorf("%q is not host:port", a)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("the port must be 1 to 65535")
	}
	if host != "" && host != "localhost" && net.ParseIP(host) == nil {
		return fmt.Errorf("the host must be empty, an IP address or localhost")
	}
	return nil
}

// sameBind: two valid addresses that would take the same port (the same host, or
// one of them every interface).
func sameBind(a, b string) bool {
	ha, pa, _ := net.SplitHostPort(a)
	hb, pb, _ := net.SplitHostPort(b)
	if pa != pb {
		return false
	}
	wild := func(h string) bool { return h == "" || h == "0.0.0.0" || h == "::" }
	return ha == hb || wild(ha) || wild(hb)
}

// checkPublicURL: empty, or http(s)://host[:port] with no path, query or credentials.
func checkPublicURL(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("must be http(s)://host[:port]")
	}
	if u.User != nil {
		return fmt.Errorf("must not hold a user name or password")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("must not have a path, query or fragment")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("the port must be 1 to 65535")
		}
	}
	return nil
}

// winAbs is an absolute Windows path (a drive's, or a UNC share's).
var winAbs = regexp.MustCompile(`^([A-Za-z]:[\\/]|\\\\|//)`)

// absAnyOS: the database keeps paths as some machine saw them, a Mac's, Linux's
// or Windows's.
func absAnyOS(p string) bool {
	return strings.HasPrefix(p, "/") || winAbs.MatchString(p)
}

func isDir(p string) error {
	fi, err := os.Stat(p)
	switch {
	case os.IsNotExist(err):
		return fmt.Errorf("%s does not exist", p)
	case err != nil:
		return fmt.Errorf("%s cannot be read", p)
	case !fi.IsDir():
		return fmt.Errorf("%s is not a folder", p)
	}
	return nil
}
