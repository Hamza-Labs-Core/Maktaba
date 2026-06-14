package hdhr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// --- fakes ---------------------------------------------------------------

type fakeRepo struct {
	dev      Device
	channels []LineupChannel
}

func (f *fakeRepo) Device(context.Context) (Device, error)          { return f.dev, nil }
func (f *fakeRepo) Lineup(context.Context) ([]LineupChannel, error) { return f.channels, nil }

type fakeStreamer struct {
	calls int
	last  uuid.UUID
}

func (s *fakeStreamer) StreamChannelTS(_ context.Context, id uuid.UUID, w io.Writer) error {
	s.calls++
	s.last = id
	_, _ = w.Write([]byte("TS"))
	return nil
}

func enabledDevice() Device {
	return Device{DeviceID: "ABCD1234", UUID: "uuid-1", FriendlyName: "Maktaba", TunerCount: 2, Enabled: true}
}

func sampleChannels() []LineupChannel {
	return []LineupChannel{
		{ID: uuid.New(), Number: 5, Name: "Kids", Slug: "kids"},
		{ID: uuid.New(), Number: 6, Name: "News 24", Slug: "news"},
	}
}

func mux(h *Handler) http.Handler {
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// --- pure builders -------------------------------------------------------

func TestBuildDiscover(t *testing.T) {
	d := buildDiscover(enabledDevice(), "http://10.0.0.5:8081", "tok123")
	if d.DeviceID != "ABCD1234" || d.TunerCount != 2 {
		t.Errorf("device fields wrong: %+v", d)
	}
	if d.BaseURL != "http://10.0.0.5:8081" || d.LineupURL != "http://10.0.0.5:8081/lineup.json" {
		t.Errorf("urls wrong: %+v", d)
	}
	if d.DeviceAuth != "tok123" {
		t.Errorf("device auth not threaded: %q", d.DeviceAuth)
	}
}

func TestBuildLineup_URLsAndGuideNumbers(t *testing.T) {
	entries := buildLineup(sampleChannels(), "http://h")
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	if entries[0].GuideNumber != "5" || entries[0].GuideName != "Kids" {
		t.Errorf("entry0 wrong: %+v", entries[0])
	}
	if entries[0].URL != "http://h/auto/v5" {
		t.Errorf("entry0 url wrong: %s", entries[0].URL)
	}
}

func TestSSDPResponse_PointsAtDeviceXML(t *testing.T) {
	resp := buildSSDPResponse("http://10.0.0.5:8081", "uuid-9")
	if !strings.Contains(resp, "LOCATION: http://10.0.0.5:8081/device.xml") {
		t.Errorf("missing LOCATION:\n%s", resp)
	}
	if !strings.HasPrefix(resp, "HTTP/1.1 200 OK\r\n") {
		t.Error("missing status line")
	}
	if !strings.Contains(resp, "USN: uuid:uuid-9::") {
		t.Error("missing USN with uuid")
	}
}

func TestIsMSearchFor(t *testing.T) {
	good := "M-SEARCH * HTTP/1.1\r\nMAN: \"ssdp:discover\"\r\nST: ssdp:all\r\n\r\n"
	if !isMSearchFor(good) {
		t.Error("should match ssdp:all M-SEARCH")
	}
	if isMSearchFor("NOTIFY * HTTP/1.1\r\n") {
		t.Error("NOTIFY must not match")
	}
	if isMSearchFor("M-SEARCH * HTTP/1.1\r\nST: urn:other\r\n") {
		t.Error("unrelated ST must not match")
	}
}

// --- lease cap -----------------------------------------------------------

func TestLeaseRegistry_CapAndRelease(t *testing.T) {
	reg := newLeaseRegistry(2)
	l1, err := reg.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.acquire(); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.acquire(); err != ErrAllTunersInUse {
		t.Errorf("3rd acquire should fail, got %v", err)
	}
	l1.Release()
	if _, err := reg.acquire(); err != nil {
		t.Errorf("after release a slot should free up: %v", err)
	}
	l1.Release() // idempotent
	if reg.count() != 2 {
		t.Errorf("count = %d, want 2", reg.count())
	}
}

// --- handler -------------------------------------------------------------

func TestHandler_DisabledIs404(t *testing.T) {
	h := New(&fakeRepo{dev: Device{Enabled: false, TunerCount: 1}}, &fakeStreamer{})
	srv := mux(h)
	for _, path := range []string{"/discover.json", "/lineup.json", "/lineup_status.json", "/auto/v5"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s when disabled = %d, want 404", path, rec.Code)
		}
	}
}

func TestHandler_DiscoverWhenEnabled(t *testing.T) {
	h := New(&fakeRepo{dev: enabledDevice(), channels: sampleChannels()}, &fakeStreamer{})
	req := httptest.NewRequest(http.MethodGet, "/discover.json", nil)
	req.Host = "10.0.0.5:8081"
	rec := httptest.NewRecorder()
	mux(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	var d DiscoverResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.BaseURL != "http://10.0.0.5:8081" {
		t.Errorf("baseURL derived wrong: %s", d.BaseURL)
	}
}

func TestHandler_AutoStreamsAndLeases(t *testing.T) {
	streamer := &fakeStreamer{}
	chans := sampleChannels()
	h := New(&fakeRepo{dev: enabledDevice(), channels: chans}, streamer)
	req := httptest.NewRequest(http.MethodGet, "/auto/v5", nil)
	rec := httptest.NewRecorder()
	mux(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("auto code = %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "video/mp2t" {
		t.Errorf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	if streamer.calls != 1 || streamer.last != chans[0].ID {
		t.Errorf("streamer not invoked for channel 5: calls=%d", streamer.calls)
	}
	// Lease released after the connection ends.
	if h.leases.count() != 0 {
		t.Errorf("lease not released, count=%d", h.leases.count())
	}
}

func TestHandler_AutoUnknownChannel404(t *testing.T) {
	h := New(&fakeRepo{dev: enabledDevice(), channels: sampleChannels()}, &fakeStreamer{})
	req := httptest.NewRequest(http.MethodGet, "/auto/v99", nil)
	rec := httptest.NewRecorder()
	mux(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown channel = %d, want 404", rec.Code)
	}
}

func TestHandler_LineupPostAcks(t *testing.T) {
	h := New(&fakeRepo{dev: enabledDevice(), channels: sampleChannels()}, &fakeStreamer{})
	req := httptest.NewRequest(http.MethodPost, "/lineup.post?scan=start", nil)
	rec := httptest.NewRecorder()
	mux(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("lineup.post = %d, want 200", rec.Code)
	}
}
