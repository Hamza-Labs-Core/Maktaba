package devices

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestValidateDownloadedRequest(t *testing.T) {
	for _, c := range []struct {
		name string
		req  DownloadedRequest
		ok   bool
	}{
		{"empty quality ok", DownloadedRequest{}, true},
		{"hd ok", DownloadedRequest{Quality: "hd", SizeBytes: 10}, true},
		{"HD case-insensitive", DownloadedRequest{Quality: "HD"}, true},
		{"audio ok", DownloadedRequest{Quality: "audio"}, true},
		{"unknown quality rejected", DownloadedRequest{Quality: "4k"}, false},
		{"negative size rejected", DownloadedRequest{SizeBytes: -1}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := validateDownloadedRequest(c.req)
			if c.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("want error, got nil")
			}
		})
	}
}

// Story 12.11 `403 not-a-device-session`: a request with no / malformed
// X-Device-ID header is not a device session.
func TestDeviceIDFromRequest(t *testing.T) {
	noHdr := httptest.NewRequest("POST", "/api/videos/x/downloaded", nil)
	if _, ok := deviceIDFromRequest(noHdr); ok {
		t.Fatalf("missing header should not be a device session")
	}

	bad := httptest.NewRequest("POST", "/api/videos/x/downloaded", nil)
	bad.Header.Set("X-Device-ID", "not-a-uuid")
	if _, ok := deviceIDFromRequest(bad); ok {
		t.Fatalf("malformed device id should be rejected")
	}

	good := httptest.NewRequest("POST", "/api/videos/x/downloaded", nil)
	want := uuid.NewString()
	good.Header.Set("X-Device-ID", want)
	got, ok := deviceIDFromRequest(good)
	if !ok || got != want {
		t.Fatalf("valid device id: got (%q,%v) want (%q,true)", got, ok, want)
	}
}
