package tv

import (
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// validDeviceID is libhdhomerun's hdhomerun_discover_validate_device_id.
func validDeviceID(id uint32) bool {
	table := [16]uint32{0xA, 0x5, 0xF, 0x6, 0x7, 0xC, 0x1, 0xB, 0x9, 0x2, 0x8, 0xD, 0x4, 0x3, 0xE, 0x0}
	c := table[(id>>28)&0xF] ^ (id>>24)&0xF ^ table[(id>>20)&0xF] ^ (id>>16)&0xF ^
		table[(id>>12)&0xF] ^ (id>>8)&0xF ^ table[(id>>4)&0xF] ^ id&0xF
	return c == 0
}

func TestNewDeviceIDHasHDHomeRunCheckDigit(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := newDeviceID()
		v, err := strconv.ParseUint(s, 16, 32)
		if err != nil || len(s) != 8 {
			t.Fatalf("%q is not 8 hex digits", s)
		}
		id := uint32(v)
		if !validDeviceID(id) {
			t.Fatalf("%s fails libhdhomerun's check digit", s)
		}
	}
	if validDeviceID(0x87C7778E) || !validDeviceID(withCheckDigit(0x87C7778E)) {
		t.Fatal("87C7778E must be invalid and repairable")
	}
}

func TestInfoOnLoopbackAdvertisesLANAddress(t *testing.T) {
	orig := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = orig })
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.IPv4(127, 0, 0, 1), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.IPv4(192, 168, 50, 177), Mask: net.CIDRMask(24, 32)},
		}, nil
	}
	_, srv := newServer(t, "")
	req := httptest.NewRequest("GET", "/api/tv/info", nil)
	req.Host = "127.0.0.1:8780"
	req = req.WithContext(principal.WithPrincipal(req.Context(), user))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, `"hdhr_address":"192.168.50.177:8782"`) {
		t.Fatalf("loopback admin should see the LAN address: %s", body)
	}
}
