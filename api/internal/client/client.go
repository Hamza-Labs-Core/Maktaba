// Package client says which app sent a request (Epic 44 story 44.2).
//
// Every Maktaba app sends `X-Maktaba-Client: <app>/<version> (<platform>;
// <device>)`, e.g. `player/1.3.0 (ios; phone)` or `web/0.3.25 (macos;
// desktop)`. The API reads it once (middleware.Client) into the request's
// context; analytics, the API's use per app and the Prometheus label read it
// back with From.
//
// The header is a label, never a right: nothing decides access by it. Every
// part is lowercased, kept to [a-z0-9._-] and 32 characters; a part that is
// missing or empty reads `unknown`. Old app builds send `native` alone, which
// reads as the app `native`.
package client

import (
	"context"
	"strings"
)

// Header is the request header every app sends.
const Header = "X-Maktaba-Client"

// Unknown is the value of a part the header did not give.
const Unknown = "unknown"

// maxPart bounds every part of the header.
const maxPart = 32

// Info is which app sent a request.
type Info struct {
	App      string // web, desktop, mobile, player, remote, tv (native: an old app build)
	Version  string // the app's version
	Platform string // ios, ipados, macos, tvos, android, androidtv, windows, linux, web
	Device   string // phone, tablet, foldable, desktop, tv
}

// None is the Info of a request without the header.
var None = Info{App: Unknown, Version: Unknown, Platform: Unknown, Device: Unknown}

// Parse reads an X-Maktaba-Client value. Whatever it cannot read is Unknown.
func Parse(v string) Info {
	info := None
	v = strings.TrimSpace(v)
	if v == "" {
		return info
	}
	head, rest, _ := strings.Cut(v, "(")
	app, version, _ := strings.Cut(strings.TrimSpace(head), "/")
	info.App = part(app)
	info.Version = part(version)
	if rest != "" {
		rest, _, _ = strings.Cut(rest, ")")
		platform, device, _ := strings.Cut(rest, ";")
		info.Platform = part(platform)
		info.Device = part(device)
	}
	return info
}

// part lowercases s and keeps [a-z0-9._-], at most maxPart characters;
// nothing left is Unknown.
func part(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(strings.TrimSpace(s)) {
		if b.Len() >= maxPart {
			break
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' {
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return Unknown
	}
	return b.String()
}

// webBundle are the apps that run the web bundle (the browser, the desktop
// shell, the mobile shell): they sign in with the session cookie.
var webBundle = map[string]bool{"web": true, "desktop": true, "mobile": true}

// IsNative reports whether the request comes from a native app (one that
// signs in for a bearer token, not the session cookie): the header names an
// app, and not one that runs the web bundle. `native` alone (an old app
// build) is one.
func (i Info) IsNative() bool {
	return i.App != "" && i.App != Unknown && !webBundle[i.App]
}

// known are the apps the Prometheus label names; any other is `other`.
var known = map[string]bool{
	"web": true, "desktop": true, "mobile": true, "player": true,
	"remote": true, "tv": true, "native": true, Unknown: true,
}

// MetricLabel is the app as a bounded label: a known app, else `other`.
func (i Info) MetricLabel() string {
	if known[i.App] {
		return i.App
	}
	return "other"
}

type ctxKey struct{}

// With returns ctx carrying info.
func With(ctx context.Context, info Info) context.Context {
	return context.WithValue(ctx, ctxKey{}, info)
}

// From is the app that sent the request of ctx (None when the middleware did
// not run).
func From(ctx context.Context) Info {
	if v, ok := ctx.Value(ctxKey{}).(Info); ok {
		return v
	}
	return None
}
