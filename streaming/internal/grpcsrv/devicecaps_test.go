package grpcsrv

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
)

// a 4K HEVC HDR10 MKV, as the pipeline's probe records one
func hevcHDR10(fb *probe.FakeBackend) *probe.Row {
	row := &probe.Row{
		VideoID: uuid.New(), LibraryID: uuid.New(), ContentHash: "hdr",
		Path: "/v/movie.mkv", Container: "matroska,webm", VideoCodec: "hevc", AudioCodec: "eac3",
		Height: 2160, Width: 3840, BitrateKbps: 40000, AudioChannels: 6, Probed: true, HasAudio: true,
		ColorTransfer: "smpte2084", PixFmt: "yuv420p10le",
	}
	fb.Set(row)
	return row
}

// Story 58.02's first acceptance case: a Fire TV whose report lists HEVC 2160p and
// HDR10 opens a 4K HEVC HDR10 MKV: direct, and no ffmpeg runs.
func TestOpenSession_DeviceCapsDirect(t *testing.T) {
	srv, fb, _ := setupServer(t)
	fake := &fakeOrchestrator{}
	srv.Transcode = fake
	srv.ResolveDir = func(string) string { return t.TempDir() }
	row := hevcHDR10(fb)
	caps := &capability.DeviceCaps{
		Containers: []string{"mp4", "mkv"},
		Video:      []capability.VideoCap{{Codec: "hevc", MaxHeight: 2160, BitDepth: 10}},
		HDR:        []string{"hdr10"},
		Audio:      []string{"aac", "eac3"},
	}
	resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(), ClientProfile: "firetv", DeviceCaps: caps,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "direct" || fake.calls != 0 {
		t.Fatalf("mode %s, ffmpeg runs %d: want direct and none", resp.Mode, fake.calls)
	}
	// the same TV without its report: the firetv baseline (H.264 1080p) transcodes it
	resp, err = srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(), ClientProfile: "firetv",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "transcode" {
		t.Fatalf("baseline mode %s, want transcode", resp.Mode)
	}
}

// A report that changes (the TV's HDMI switched to a receiver without HDR) opens a
// new session: one made for the earlier report is never handed back.
func TestOpenSession_ChangedReportOpensANewSession(t *testing.T) {
	srv, fb, _ := setupServer(t)
	row := hevcHDR10(fb)
	user := uuid.New().String()
	open := func(caps *capability.DeviceCaps) OpenSessionResponse {
		t.Helper()
		resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
			VideoID: row.VideoID.String(), UserID: user, ClientProfile: "firetv", DeviceCaps: caps,
		})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	hdr := &capability.DeviceCaps{Containers: []string{"mkv"}, Video: []capability.VideoCap{{Codec: "hevc", MaxHeight: 2160}}, HDR: []string{"hdr10"}, Audio: []string{"eac3"}}
	sdr := &capability.DeviceCaps{Containers: []string{"mkv"}, Video: []capability.VideoCap{{Codec: "hevc", MaxHeight: 2160}}, HDR: []string{}, Audio: []string{"eac3"}}
	a, b := open(hdr), open(hdr)
	if a.SessionID != b.SessionID {
		t.Fatal("the same report should share its session")
	}
	c := open(sdr)
	if c.SessionID == a.SessionID || c.Mode != "transcode" {
		t.Fatalf("changed report: session %s mode %s; want a new transcode", c.SessionID, c.Mode)
	}
}

// device_caps rides the JSON gRPC request; keys this home does not know are
// ignored, a wrong type is refused.
func TestHandleOpenSession_DeviceCaps(t *testing.T) {
	srv, fb, _ := setupServer(t)
	row := hevcHDR10(fb)
	out, err := srv.handleOpenSession(context.Background(), map[string]any{
		"video_id": row.VideoID.String(), "user_id": uuid.New().String(), "client_profile": "tizen",
		"device_caps": map[string]any{
			"containers": []any{"mkv"},
			"video":      []any{map[string]any{"codec": "hevc", "max_height": 2160.0}},
			"hdr":        []any{"hdr10"},
			"audio":      []any{"eac3"},
			"later_key":  "ignored",
		},
	})
	if err != nil || out["error"] != nil {
		t.Fatalf("err %v %v", err, out["error"])
	}
	if out["mode"] != "direct" {
		t.Fatalf("mode %v, want direct", out["mode"])
	}
	out, _ = srv.handleOpenSession(context.Background(), map[string]any{
		"video_id": row.VideoID.String(), "user_id": uuid.New().String(), "client_profile": "tizen",
		"device_caps": map[string]any{"containers": "mkv"},
	})
	if out["error"] == nil {
		t.Fatal("a device_caps of the wrong shape was accepted")
	}
}

func TestSourceOf(t *testing.T) {
	src := SourceOf(&probe.Row{ColorTransfer: "arib-std-b67", PixFmt: "yuv420p10le", DVProfile: 8, DVCompat: 4, FPS: 50})
	if src.HDR != "hlg" || src.BitDepth != 10 || src.DVProfile != 8 || src.DVCompat != 4 || src.FPS != 50 {
		t.Fatalf("%+v", src)
	}
	if s := SourceOf(&probe.Row{PixFmt: "yuv420p"}); s.HDR != "" || s.BitDepth != 8 {
		t.Fatalf("%+v", s)
	}
}

// openWith opens a session of row for profile with caps on a server whose
// ffmpeg is a fake, returning the response and the job it was handed.
func openWith(t *testing.T, row *probe.Row, profile string, caps *capability.DeviceCaps, format string) (OpenSessionResponse, *fakeOrchestrator, *Server) {
	t.Helper()
	srv, fb, _ := setupServer(t)
	fb.Set(row)
	fake := &fakeOrchestrator{}
	srv.Transcode = fake
	dir := t.TempDir()
	srv.ResolveDir = func(id string) string { return dir + "/" + id }
	resp, err := srv.OpenSession(context.Background(), OpenSessionRequest{
		VideoID: row.VideoID.String(), UserID: uuid.New().String(), ClientProfile: profile, DeviceCaps: caps, Format: format,
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp, fake, srv
}

// Story 58.02's acceptance: a Cast receiver listing fmp4 only, an HEVC source to
// remux: a remux job in fMP4 (no transcode slot taken), its session's
// subtitle renditions mapped on fMP4's timestamps.
func TestOpenSession_CastHEVCRemuxInFMP4(t *testing.T) {
	_, fb, _ := setupServer(t)
	row := hevcHDR10(fb)
	caps := &capability.DeviceCaps{Video: []capability.VideoCap{{Codec: "hevc", MaxHeight: 2160}}, HDR: []string{"hdr10"},
		Audio: []string{"aac", "eac3"}, HLSSegments: []string{"fmp4"}}
	resp, fake, srv := openWith(t, row, "cast", caps, "")
	if resp.Mode != "remux" || fake.calls != 1 || fake.lastJob.Remux == nil {
		t.Fatalf("mode %s calls %d job %+v", resp.Mode, fake.calls, fake.lastJob)
	}
	p := fake.lastJob.Remux
	if p.Segments != "fmp4" || p.VideoCodec != "hevc" || p.AudioCodec != "" || p.DropDV || p.VideoRange != "PQ" || !p.TenBit {
		t.Fatalf("plan %+v", p)
	}
	if resp.ManifestPath != "/stream/"+resp.SessionID+"/manifest.m3u8" {
		t.Fatalf("manifest %s", resp.ManifestPath)
	}
	if srv.Allocator.Used() != 0 {
		t.Fatal("a remux took a transcode slot")
	}
	id, _ := uuid.Parse(resp.SessionID)
	row2, _, _ := srv.Sessions.Get(context.Background(), id)
	if row2.Segments != "fmp4" || row2.Mode != "remux" {
		t.Fatalf("session row %+v", row2)
	}
}

// A Samsung TV (no dv-* in its report) and a Dolby Vision 8.1 source: a remux to
// the HDR10 base layer, not a transcode; TrueHD as EAC3 beside it.
func TestOpenSession_TizenDV81RemuxToHDR10(t *testing.T) {
	_, fb, _ := setupServer(t)
	row := hevcHDR10(fb)
	row.DVProfile, row.DVCompat, row.AudioCodec, row.AudioChannels = 8, 1, "truehd", 8
	caps := &capability.DeviceCaps{Containers: []string{"mp4", "mkv"}, Video: []capability.VideoCap{{Codec: "hevc", MaxHeight: 2160}},
		HDR: []string{"hdr10", "hlg"}, Audio: []string{"aac", "ac3", "eac3"}, HLSSegments: []string{"ts", "fmp4"}}
	resp, fake, _ := openWith(t, row, "tizen", caps, "")
	if resp.Mode != "remux" || fake.lastJob.Remux == nil {
		t.Fatalf("mode %s job %+v", resp.Mode, fake.lastJob)
	}
	if p := fake.lastJob.Remux; !p.DropDV || p.AudioCodec != "eac3" || p.Segments != "fmp4" || p.VideoRange != "PQ" {
		t.Fatalf("plan %+v", p)
	}
}

// A Roku report claiming AV1 inside HLS: the session goes by DASH, the video copied.
func TestOpenSession_RokuAV1ByDASH(t *testing.T) {
	_, fb, _ := setupServer(t)
	row := &probe.Row{VideoID: uuid.New(), LibraryID: uuid.New(), Path: "/v/a.mkv", Container: "matroska,webm",
		VideoCodec: "av1", AudioCodec: "aac", AudioChannels: 2, Height: 2160, Width: 3840, BitrateKbps: 15000, Probed: true, HasAudio: true}
	fb.Set(row)
	yes := true
	caps := &capability.DeviceCaps{Video: []capability.VideoCap{{Codec: "av1", MaxHeight: 2160}}, HLSSegments: []string{"ts", "fmp4"}, DASH: &yes}
	resp, fake, _ := openWith(t, row, "roku", caps, "hls")
	if resp.Mode != "remux" || fake.lastJob.Format != "dash" || !strings.HasSuffix(resp.ManifestPath, ".mpd") {
		t.Fatalf("mode %s format %s manifest %s", resp.Mode, fake.lastJob.Format, resp.ManifestPath)
	}
}

// The older profiles never remux: a container they do not play is an HLS
// transcode, as it always was (no existing client sees a change).
func TestOpenSession_OlderProfileRemuxStaysATranscode(t *testing.T) {
	_, fb, _ := setupServer(t)
	row := &probe.Row{VideoID: uuid.New(), LibraryID: uuid.New(), Path: "/v/a.mkv", Container: "matroska,webm",
		VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 6000, Probed: true}
	fb.Set(row)
	resp, fake, _ := openWith(t, row, "ios-native", nil, "")
	if resp.Mode != "transcode" || fake.lastJob.Remux != nil || fake.lastJob.Segments != "" {
		t.Fatalf("mode %s job %+v", resp.Mode, fake.lastJob)
	}
}
