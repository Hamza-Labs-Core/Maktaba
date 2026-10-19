package handlers

import (
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// Story 31.3: a canonical smb:// path is served from this machine's
// mount of the share (the default share map).
func TestDirect_CanonicalPathResolvesThroughTheShareMap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's /v/x.mp4 is not an absolute path on Windows")
	}
	_, row, _, h := setupHandler(t)
	row.Path = "smb://nas/Originals/x.mp4" // the opener holds /v/x.mp4
	t.Cleanup(func() { pathmap.SetDefault(pathmap.Map{}) })
	pathmap.SetDefault(pathmap.Parse("smb://nas/Originals/=>/v/"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDirect_UnmountedShareIsMissing(t *testing.T) {
	_, row, _, h := setupHandler(t)
	row.Path = "smb://nas/Elsewhere/x.mp4"
	t.Cleanup(func() { pathmap.SetDefault(pathmap.Map{}) })
	pathmap.SetDefault(pathmap.Parse("smb://nas/Originals/=>/v/"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/direct/"+row.VideoID.String(), nil))
	if rec.Code != 404 {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}
