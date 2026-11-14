package streaming

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// T06: device_caps with keys this home does not know: the unknown ones are
// ignored, the known ones kept and passed on to the streaming service.
func TestOpenSession_DeviceCapsUnknownKeysIgnored(t *testing.T) {
	h, _, svc := offlineRig(t, true)
	body := `{"video_id":"` + vHere + `","client_profile":"firetv","device_caps":{` +
		`"containers":["mp4","mkv"],"video":[{"codec":"hevc","max_height":2160,"hdr_layers":2}],` +
		`"hdr":["hdr10"],"dash":true,"panel":"oled","max_bitrate_kbps":80000}}`
	rec := call(h, as("u-amal", "lib1"), http.MethodPost, "/api/stream/sessions", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	}
	if len(svc.opened) != 1 {
		t.Fatalf("opened %d", len(svc.opened))
	}
	var sent map[string]any
	if err := json.Unmarshal(svc.opened[0].DeviceCaps, &sent); err != nil {
		t.Fatalf("device_caps passed on: %v (%s)", err, svc.opened[0].DeviceCaps)
	}
	if _, ok := sent["panel"]; ok {
		t.Errorf("an unknown key was passed on: %s", svc.opened[0].DeviceCaps)
	}
	video := sent["video"].([]any)[0].(map[string]any)
	if _, ok := video["hdr_layers"]; ok || video["codec"] != "hevc" || video["max_height"] != 2160.0 {
		t.Errorf("video entry: %v", video)
	}
	if sent["dash"] != true || sent["max_bitrate_kbps"] != 80000.0 || len(sent["containers"].([]any)) != 2 {
		t.Errorf("known keys lost: %s", svc.opened[0].DeviceCaps)
	}
	// the rest of the body stays strict: an unknown top-level key is still refused
	if rec := call(h, as("u-amal", "lib1"), http.MethodPost, "/api/stream/sessions",
		`{"video_id":"`+vHere+`","later_field":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown top-level key: %d, want 400", rec.Code)
	}
}

func TestOpenSession_DeviceCapsRefusesAWrongShape(t *testing.T) {
	h, _, svc := offlineRig(t, true)
	for _, caps := range []string{`{"containers":"mp4"}`, `["mp4"]`, `{"video":[{"codec":7}]}`,
		`{"hdr":[` + strings.Repeat(`"hdr10",`, 40) + `"hlg"]}`} {
		rec := call(h, as("u-amal", "lib1"), http.MethodPost, "/api/stream/sessions",
			`{"video_id":"`+vHere+`","device_caps":`+caps+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", caps, rec.Code)
		}
	}
	if len(svc.opened) != 0 {
		t.Fatalf("a refused report reached the streaming service")
	}
	// no report at all is the profile's baseline: nothing is passed on
	if rec := call(h, as("u-amal", "lib1"), http.MethodPost, "/api/stream/sessions", `{"video_id":"`+vHere+`"}`); rec.Code != http.StatusCreated ||
		svc.opened[0].DeviceCaps != nil {
		t.Fatalf("no report: %d %s", rec.Code, svc.opened[0].DeviceCaps)
	}
}

// fakeSigner signs a URL with a token naming its path (enough to tell which
// path each one was minted for).
type fakeSigner struct{ paths []string }

func (f *fakeSigner) SignStreamURL(rawURL, sessionID, _, _ string, ttl time.Duration) (string, time.Time, error) {
	f.paths = append(f.paths, rawURL)
	return rawURL + "?sig=" + url.QueryEscape("tok:"+sessionID+":"+rawURL), time.Now().Add(ttl), nil
}

// Every session, direct play too, carries stream_sig: a token for its own paths
// (subtitles, chapters), for a TV player that sends no header (story 58.02).
func TestOpenSession_StreamSigOnEverySession(t *testing.T) {
	off := &fakeOffline{down: true, videos: map[string]catalogVideo{vHere: {lib: "lib1", dur: 1800}}}
	hd := &Handler{DB: deadDB(), Service: &fakeService{}, Offline: off, Signer: &fakeSigner{}}
	h := chi.NewRouter()
	hd.Mount(h)
	rec := call(h, as("u-amal", "lib1"), http.MethodPost, "/api/stream/sessions", `{"video_id":"`+vHere+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	}
	var s OpenSessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if s.Mode != "direct" || !strings.Contains(s.DirectURL, "?sig=") {
		t.Fatalf("direct url not signed: %+v", s)
	}
	want := "tok:" + s.SessionID + ":/stream/" + s.SessionID + "/chapters.json"
	if s.StreamSig != want {
		t.Fatalf("stream_sig %q, want the session's own token %q", s.StreamSig, want)
	}
}

// T07, the API's half: each shared vector's report passes readDeviceCaps with
// nothing lost (an app's report reaches the streaming service as it sent it).
func TestReadDeviceCaps_SharedVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "playback", "caps-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Vectors []struct {
			ID         string          `json:"id"`
			DeviceCaps json.RawMessage `json:"device_caps"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	norm := func(b []byte) map[string]any {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for k, v := range m {
			if v == nil {
				delete(m, k) // null is a field left out
			}
		}
		return m
	}
	for _, v := range file.Vectors {
		out, err := readDeviceCaps(v.DeviceCaps)
		if err != nil {
			t.Errorf("%s: %v", v.ID, err)
			continue
		}
		if !reflect.DeepEqual(norm(v.DeviceCaps), norm(out)) {
			t.Errorf("%s: report changed on the way:\n in  %s\n out %s", v.ID, v.DeviceCaps, out)
		}
	}
	// an empty list stays the device's "none"
	out, _ := readDeviceCaps(json.RawMessage(`{"passthrough":[]}`))
	if !strings.Contains(string(out), `"passthrough":[]`) {
		t.Errorf("an empty list was dropped: %s", out)
	}
}
