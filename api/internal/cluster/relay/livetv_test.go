package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The Live TV calls plex_livetv makes (story 54.35), exactly as PlexClient sends them
// (recorded from integrations.plex with a capturing transport).
var liveTVCalls = [][2]string{
	{"GET", "/livetv/dvrs"},
	{"GET", "/media/grabbers/devices"},
	{"POST", "/media/grabbers/devices?uri=http%3A%2F%2F192.168.1.5%3A5004"},
	{"POST", "/livetv/dvrs?language=ara&device=device%3A%2F%2Ftv.plex.grabbers.hdhomerun%2FMAKTABA01&lineup=lineup%3A%2F%2Ftv.plex.providers.epg.xmltv%2Fhttp%253A%252F%252F192.168.1.5%253A8080%252Fapi%252Ftv%252Fxmltv.xml%23%25D9%2585%25D9%2583%25D8%25AA%25D8%25A8%25D8%25A9%2520TV&lineupTitle=%D9%85%D9%83%D8%AA%D8%A8%D8%A9%20TV"},
	{"PUT", "/livetv/dvrs/12/lineups?lineup=lineup%3A%2F%2Ftv.plex.providers.epg.xmltv%2Fhttp%253A%252F%252F192.168.1.5%253A8080%252Fapi%252Ftv%252Fxmltv.xml%23%25D9%2585%25D9%2583%25D8%25AA%25D8%25A8%25D8%25A9%2520TV"},
	{"PUT", "/livetv/dvrs/12/devices/34"},
	{"DELETE", "/livetv/dvrs/12"},
	{"DELETE", "/livetv/dvrs/12/lineups?lineup=lineup%3A%2F%2Ftv.plex.providers.epg.xmltv%2Fhttp%253A%252F%252F192.168.1.5%253A8080%252Fapi%252Ftv%252Fxmltv.xml%23%25D9%2585%25D9%2583%25D8%25AA%25D8%25A8%25D8%25A9%2520TV"},
	{"DELETE", "/livetv/dvrs/12/devices/34"},
	{"DELETE", "/media/grabbers/devices/34"},
	{"PUT", "/media/grabbers/devices/34?title=%D9%85%D9%83%D8%AA%D8%A8%D8%A9&enabled=1"},
	{"POST", "/livetv/dvrs/12/reloadGuide"},
	{"PUT", "/media/grabbers/devices/34/channelmap?channelsEnabled=101%2C102%2C150&channelMapping%5B101%5D=101&channelMappingByKey%5B101%5D=101&channelMapping%5B102%5D=102&channelMappingByKey%5B102%5D=102&channelMapping%5B150%5D=150&channelMappingByKey%5B150%5D=150"},
}

var home = Caller{Kind: "home", WorkerID: "tv.maktaba.plex"}

// Every Live TV call is allowed for the home's own plex_livetv, and never for a node.
func TestLiveTVCallsAreTheHomesOnly(t *testing.T) {
	for _, c := range liveTVCalls {
		if _, _, _, err := Plex.MatchFor(true, c[0], c[1]); err != nil {
			t.Errorf("home: %s %s: %v", c[0], c[1], err)
		}
		if _, _, _, err := Plex.MatchFor(false, c[0], c[1]); err == nil {
			t.Errorf("node: %s %s was allowed", c[0], c[1])
		}
	}
}

func TestLiveTVRefusals(t *testing.T) {
	for _, c := range [][2]string{
		// a channel map with a parameter of another family, or a mapping that is no number
		{"PUT", "/media/grabbers/devices/34/channelmap?channelsEnabled=101&channelMapping%5B101%5D=101&other=1"},
		{"PUT", "/media/grabbers/devices/34/channelmap?channelsEnabled=101&channelMapping%5B101%5D=x"},
		{"PUT", "/media/grabbers/devices/34/channelmap?channelMapping%5B101%5D=101"}, // channelsEnabled required
		// the token as a query parameter, whatever the case
		{"GET", "/livetv/dvrs?X-Plex-Token=abc"},
		{"GET", "/livetv/dvrs?x-plex-token=abc"},
		// a tuner that is not a URL, a device that is not an HDHomeRun, a guide that is not XMLTV
		{"POST", "/media/grabbers/devices?uri=file%3A%2F%2F%2Fetc%2Fpasswd"},
		{"POST", "/livetv/dvrs?language=ara&device=device%3A%2F%2Ftv.plex.grabbers.other%2Fx&lineup=lineup%3A%2F%2Ftv.plex.providers.epg.xmltv%2Fx&lineupTitle=t"},
		{"PUT", "/livetv/dvrs/12/lineups?lineup=lineup%3A%2F%2Ftv.plex.providers.epg.gracenote%2Fx"},
		// a title without enabled=1, a DVR deleted by name
		{"PUT", "/media/grabbers/devices/34?title=x"},
		{"DELETE", "/livetv/dvrs/abc"},
		// other Live TV and server paths stay out
		{"GET", "/livetv/sessions"},
		{"PUT", "/:/prefs?LiveTVEnabled=0"},
	} {
		if _, _, _, err := Plex.MatchFor(true, c[0], c[1]); err == nil {
			t.Errorf("%s %s was allowed", c[0], c[1])
		}
	}
}

// A home caller (the plugin route) is served with the relay switched off and with no
// membership: those are a node's. The token reaches Plex and never comes back, in Live
// TV's answers either.
func TestTheHomesPluginIsServedAndNeverSeesTheToken(t *testing.T) {
	h := newHarness(t)
	h.set(func(h *harness) {
		h.settings.Enabled = false
		h.member = fmt.Errorf("not a member")
	})
	h.plex.handle = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"MediaContainer":{"Dvr":[{"key":"12","lineup":"lineup://x?X-Plex-Token=`+secret+
			`","Device":[{"key":"34","uri":"http://t/?X-Plex-Token=`+secret+`","note":"`+secret+`"}]}]}}`)
	}
	for _, c := range liveTVCalls {
		sink := &bufSink{}
		res := h.door.Serve(context.Background(), home, Request{Ext: "plex", Method: c[0], Target: c[1],
			Accept: "application/json"}, sink)
		if res.Err != nil || res.Status != 200 {
			t.Fatalf("%s %s: %+v", c[0], c[1], res)
		}
		if strings.Contains(sink.String(), secret) || strings.Contains(strings.ToLower(sink.String()), "x-plex-token") {
			t.Fatalf("%s %s: the answer carries the token: %s", c[0], c[1], sink.String())
		}
	}
	for _, seen := range h.plex.calls() {
		if !strings.HasSuffix(seen, " token="+secret) {
			t.Fatalf("plex was called without the token: %s", seen)
		}
	}
	// a node is still refused while the relay is off
	res, _ := h.call("GET", "/identity")
	if res.Err == nil || res.Err.Code != CodeOff {
		t.Fatalf("a node with the relay off: %+v", res)
	}
}
