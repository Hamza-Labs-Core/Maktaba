package viewing

import (
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// ClientHeader is the app's label (Epic 44): "<app>/<version> (<platform>; <device>)",
// e.g. "player/1.3.0 (ios; phone)". It is a label, never trusted for rights.
const ClientHeader = "X-Maktaba-Client"

// ViewerHeader is the person the web port's gate signed in (the API's /api/auth/me
// user_id; cmd/maktaba-server stream_gate.go). It is trusted only on a loopback
// connection: the gate on this host. Anything else could have written it.
const ViewerHeader = "X-Maktaba-Viewer"

// maxLabel bounds each part of a client label.
const maxLabel = 32

// Client is the app a viewer watches in (empty parts: not known).
type Client struct {
	App      string
	Version  string
	Platform string
	Device   string
}

// label is the client as one string (part of an app viewer's key).
func (c Client) label() string {
	return c.App + "/" + c.Version + "/" + c.Platform + "/" + c.Device
}

// ParseClient reads an X-Maktaba-Client value. An app part that is missing or not a
// plain label reads "unknown"; the others are left empty. "native" alone (the
// builds before Epic 44) reads app=native.
func ParseClient(v string) Client {
	v = strings.TrimSpace(v)
	if v == "" {
		return Client{}
	}
	head, rest := v, ""
	if i := strings.IndexByte(v, '('); i >= 0 {
		head, rest = v[:i], v[i+1:]
		if j := strings.IndexByte(rest, ')'); j >= 0 {
			rest = rest[:j]
		}
	}
	head = strings.TrimSpace(head)
	app, version := head, ""
	if i := strings.IndexByte(head, '/'); i >= 0 {
		app, version = head[:i], head[i+1:]
	}
	c := Client{App: clean(app), Version: clean(version)}
	if c.App == "" {
		c.App = "unknown"
	}
	if rest != "" {
		parts := strings.SplitN(rest, ";", 2)
		c.Platform = clean(parts[0])
		if len(parts) == 2 {
			c.Device = clean(parts[1])
		}
	}
	return c
}

// clean lowercases a label part; one that is too long or holds anything but
// [a-z0-9._-] is dropped.
func clean(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > maxLabel {
		return ""
	}
	for _, r := range s {
		if !labelRune(r) {
			return ""
		}
	}
	return s
}

func labelRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
}

// FromUserAgent is a coarse client from a User-Agent, for a player that does not
// send X-Maktaba-Client: a browser is the web; the platform and device from the
// usual words.
func FromUserAgent(ua string) Client {
	if strings.TrimSpace(ua) == "" {
		return Client{}
	}
	l := strings.ToLower(ua)
	c := Client{App: "unknown"}
	if strings.HasPrefix(l, "mozilla/") {
		c.App = "web"
	}
	switch {
	case strings.Contains(l, "appletv") || strings.Contains(l, "apple tv") || strings.Contains(l, "tvos"):
		c.Platform, c.Device = "tvos", "tv"
	case strings.Contains(l, "ipad"):
		c.Platform, c.Device = "ipados", "tablet"
	case strings.Contains(l, "iphone") || strings.Contains(l, "ipod"):
		c.Platform, c.Device = "ios", "phone"
	case strings.Contains(l, "android") && (strings.Contains(l, " tv") || strings.Contains(l, "aft")):
		c.Platform, c.Device = "androidtv", "tv"
	case strings.Contains(l, "android"):
		c.Platform, c.Device = "android", "tablet"
		if strings.Contains(l, "mobile") {
			c.Device = "phone"
		}
	case strings.Contains(l, "macintosh") || strings.Contains(l, "mac os x"):
		c.Platform, c.Device = "macos", "desktop"
	case strings.Contains(l, "windows"):
		c.Platform, c.Device = "windows", "desktop"
	case strings.Contains(l, "linux") || strings.Contains(l, "x11"):
		c.Platform, c.Device = "linux", "desktop"
	}
	return c
}

// Viewer is who fetched a channel segment.
type Viewer struct {
	// User is the signed-in person (uuid.Nil: not known; such a viewer is
	// counted but never recorded).
	User   uuid.UUID
	Client Client
	// Addr is the client's address (the key of a viewer with no person).
	Addr string
}

// FromRequest is the viewer of a front-door request: the person from the gate's
// ViewerHeader on a loopback connection, the app from X-Maktaba-Client (else the
// User-Agent), the address from X-Forwarded-For (else the connection).
func FromRequest(r *http.Request) Viewer {
	v := Viewer{Addr: remoteHost(r.RemoteAddr)}
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		v.Addr = strings.TrimSpace(strings.Split(f, ",")[0])
	}
	if h := r.Header.Get(ViewerHeader); h != "" && loopback(r.RemoteAddr) {
		if id, err := uuid.Parse(h); err == nil {
			v.User = id
		}
	}
	if h := r.Header.Get(ClientHeader); h != "" {
		v.Client = ParseClient(h)
	} else {
		v.Client = FromUserAgent(r.UserAgent())
	}
	return v
}

func remoteHost(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func loopback(remoteAddr string) bool {
	ip := net.ParseIP(strings.Trim(remoteHost(remoteAddr), "[]"))
	return ip != nil && ip.IsLoopback()
}
