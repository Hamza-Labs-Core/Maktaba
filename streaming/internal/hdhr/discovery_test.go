package hdhr

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Tests here never bind a non-loopback address (Windows asks to allow every
// test binary that does): sockets are 127.0.0.1:0 or injected.

// --- known-good bytes ----------------------------------------------------

// The discover request libhdhomerun releases before 2022 broadcast
// (hdhomerun_discover_send_request: DEVICE_TYPE tuner + DEVICE_ID wildcard,
// sealed by hdhomerun_pkt_seal_frame): the 20-byte frame seen in every
// Wireshark capture of HDHomeRun discovery, CRC 0x357F504E little-endian.
const libhdhrRequestLegacy = "0002000c0104000000010204ffffffff4e507f35"

// The current libhdhomerun request: the wildcard id is no longer sent.
const libhdhrRequestCurrent = "00020006010400000001393077e7"

// A MULTI_TYPE request for tuners and storage (HDHOMERUN_DEVICE_TYPE_STORAGE).
const libhdhrRequestMulti = "0002000a2d0800000001000000054f63391f"

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// libhdhrCRC is hdhomerun_pkt_calc_crc (hdhomerun_pkt.c) transliterated.
func libhdhrCRC(b []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, c := range b {
		x := byte(crc) ^ c
		crc >>= 8
		for i, k := range []uint32{0x77073096, 0xEE0E612C, 0x076DC419, 0x0EDB8832,
			0x1DB71064, 0x3B6E20C8, 0x76DC4190, 0xEDB88320} {
			if x&(1<<uint(i)) != 0 {
				crc ^= k
			}
		}
	}
	return crc ^ 0xFFFFFFFF
}

// libhdhrValidate is hdhomerun_discover_validate_device_id transliterated.
func libhdhrValidate(id uint32) bool {
	t := [16]uint32{0xA, 0x5, 0xF, 0x6, 0x7, 0xC, 0x1, 0xB, 0x9, 0x2, 0x8, 0xD, 0x4, 0x3, 0xE, 0x0}
	var c uint32
	c ^= t[(id>>28)&0x0F]
	c ^= (id >> 24) & 0x0F
	c ^= t[(id>>20)&0x0F]
	c ^= (id >> 16) & 0x0F
	c ^= t[(id>>12)&0x0F]
	c ^= (id >> 8) & 0x0F
	c ^= t[(id>>4)&0x0F]
	c ^= (id >> 0) & 0x0F
	return c == 0
}

func TestHDHRPacket_EncodeMatchesLibhdhomerun(t *testing.T) {
	cases := map[string][]byte{
		libhdhrRequestLegacy:  unhex(t, "0104000000010204ffffffff"),
		libhdhrRequestCurrent: unhex(t, "010400000001"),
		libhdhrRequestMulti:   unhex(t, "2d080000000100000005"),
	}
	for want, payload := range cases {
		if got := hex.EncodeToString(encodeHDHRPacket(hdhrTypeDiscoverReq, payload)); got != want {
			t.Errorf("encode = %s, want %s", got, want)
		}
		typ, p, err := decodeHDHRPacket(unhex(t, want))
		if err != nil || typ != hdhrTypeDiscoverReq || !bytes.Equal(p, payload) {
			t.Errorf("decode %s = %x %x %v", want, typ, p, err)
		}
	}
}

func TestHDHRPacket_CRCIsLibhdhomerunCRC(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		b := make([]byte, r.Intn(300))
		r.Read(b)
		if libhdhrCRC(b) != crc32.ChecksumIEEE(b) {
			t.Fatalf("CRC differs for %x", b)
		}
	}
}

func TestHDHRPacket_DecodeRejectsBadFrames(t *testing.T) {
	good := unhex(t, libhdhrRequestLegacy)
	bad := append([]byte(nil), good...)
	bad[len(bad)-1] ^= 0xFF
	if _, _, err := decodeHDHRPacket(bad); err != errBadCRC {
		t.Errorf("bad CRC: %v", err)
	}
	if _, _, err := decodeHDHRPacket(good[:10]); err != errShortPacket {
		t.Errorf("truncated: %v", err)
	}
	if _, _, err := decodeHDHRPacket(nil); err != errShortPacket {
		t.Errorf("empty: %v", err)
	}
}

func TestHDHRPacket_TwoByteVarLen(t *testing.T) {
	v := bytes.Repeat([]byte{'a'}, 200)
	b := appendTLV(nil, hdhrTagBaseURL, v)
	// hdhomerun_pkt_write_var_length: (200 | 0x80) & 0xFF, 200 >> 7
	if b[0] != hdhrTagBaseURL || b[1] != 0xC8 || b[2] != 0x01 || len(b) != 203 {
		t.Fatalf("header % x", b[:3])
	}
	tl := parseTLVs(b)
	if len(tl) != 1 || !bytes.Equal(tl[0].Value, v) {
		t.Fatalf("round trip: %+v", tl)
	}
	if parseTLVs([]byte{0x2A, 0x05, 'a'}) != nil {
		t.Error("truncated TLV must not parse")
	}
}

// --- device id check digit -----------------------------------------------

func TestDeviceID_CheckDigit(t *testing.T) {
	// The id the Mac provisioned before this check: libhdhomerun rejects it.
	if ValidDeviceID(0x87C7778E) || libhdhrValidate(0x87C7778E) {
		t.Fatal("87C7778E should fail libhdhomerun's check")
	}
	fixed, changed := RepairDeviceID("87C7778E")
	if !changed || fixed != "87C77788" {
		t.Fatalf("repair = %s %v, want 87C77788", fixed, changed)
	}
	if id, _ := ParseDeviceID(fixed); !libhdhrValidate(id) {
		t.Fatal("repaired id fails libhdhomerun's check")
	}
	if s, changed := RepairDeviceID(fixed); changed || s != fixed {
		t.Error("a valid id must stay as is")
	}
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 2000; i++ {
		id := r.Uint32()
		if ValidDeviceID(id) != libhdhrValidate(id) {
			t.Fatalf("ValidDeviceID(%08X) disagrees with libhdhomerun", id)
		}
		if !libhdhrValidate(WithCheckDigit(id)) {
			t.Fatalf("WithCheckDigit(%08X) invalid", id)
		}
	}
	for i := 0; i < 200; i++ {
		id, err := ParseDeviceID(newDeviceID())
		if err != nil || !libhdhrValidate(id) {
			t.Fatalf("newDeviceID gave an invalid id: %v", err)
		}
	}
}

// --- discover request -> reply -------------------------------------------

func replyTags(t *testing.T, b []byte) map[byte][]byte {
	t.Helper()
	typ, p, err := decodeHDHRPacket(b)
	if err != nil || typ != hdhrTypeDiscoverRpy {
		t.Fatalf("reply frame: type %x err %v", typ, err)
	}
	out := map[byte][]byte{}
	for _, tl := range parseTLVs(p) {
		out[tl.Tag] = tl.Value
	}
	return out
}

func TestDiscoverRequest_GetsTheRightReply(t *testing.T) {
	info := hdhrDiscoverInfo{DeviceID: 0x87C77788, TunerCount: 20,
		BaseURL: "http://192.168.50.177:8081", LineupURL: "http://192.168.50.177:8081/lineup.json"}
	for _, req := range []string{libhdhrRequestLegacy, libhdhrRequestCurrent, libhdhrRequestMulti} {
		reply := handleDiscoverPacket(unhex(t, req), info)
		if reply == nil {
			t.Fatalf("no reply to %s", req)
		}
		tags := replyTags(t, reply)
		if binary.BigEndian.Uint32(tags[hdhrTagDeviceType]) != hdhrDeviceTypeTuner {
			t.Error("device type")
		}
		if len(tags[hdhrTagDeviceID]) != 4 || binary.BigEndian.Uint32(tags[hdhrTagDeviceID]) != 0x87C77788 {
			t.Error("device id")
		}
		if !bytes.Equal(tags[hdhrTagTunerCount], []byte{20}) {
			t.Error("tuner count")
		}
		if string(tags[hdhrTagBaseURL]) != info.BaseURL || string(tags[hdhrTagLineupURL]) != info.LineupURL {
			t.Errorf("urls %q %q", tags[hdhrTagBaseURL], tags[hdhrTagLineupURL])
		}
		if _, ok := tags[hdhrTagDeviceAuthStr]; ok {
			t.Error("empty DeviceAuth must not be sent")
		}
	}
	// a request for another device id, for storage only, or a reply: silence
	other := encodeHDHRPacket(hdhrTypeDiscoverReq,
		appendTLVU32(appendTLVU32(nil, hdhrTagDeviceType, hdhrDeviceTypeTuner), hdhrTagDeviceID, 0x10100000))
	storage := encodeHDHRPacket(hdhrTypeDiscoverReq, appendTLVU32(nil, hdhrTagDeviceType, 5))
	rpy := encodeHDHRPacket(hdhrTypeDiscoverRpy, unhex(t, "010400000001"))
	for name, b := range map[string][]byte{"other id": other, "storage": storage, "reply": rpy, "junk": []byte("M-SEARCH")} {
		if handleDiscoverPacket(b, info) != nil {
			t.Errorf("%s: must not reply", name)
		}
	}
	// our own id, and a DeviceAuth when there is one
	mine := encodeHDHRPacket(hdhrTypeDiscoverReq,
		appendTLVU32(appendTLVU32(nil, hdhrTagDeviceType, hdhrDeviceTypeTuner), hdhrTagDeviceID, 0x87C77788))
	info.DeviceAuth = "tok"
	if tags := replyTags(t, handleDiscoverPacket(mine, info)); string(tags[hdhrTagDeviceAuthStr]) != "tok" {
		t.Error("targeted request / DeviceAuth")
	}
}

// --- advertised base ------------------------------------------------------

func lanAddrs() ([]net.Addr, error) {
	return []net.Addr{
		&net.IPNet{IP: net.IPv4(127, 0, 0, 1), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.IPv4(192, 168, 50, 177), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.IPv4(10, 8, 0, 1), Mask: net.CIDRMask(24, 32)},
	}, nil
}

func TestAdvertiser_AbsoluteBasePerInterface(t *testing.T) {
	a := Advertiser{HTTPAddr: ":8081", Addrs: lanAddrs,
		Route: func(net.IP) net.IP { return net.IPv4(192, 168, 50, 177) }}
	cases := map[string]string{
		"192.168.50.20": "http://192.168.50.177:8081", // Plex on the LAN
		"10.8.0.7":      "http://10.8.0.1:8081",       // a second network (VPN)
		"172.20.1.1":    "http://192.168.50.177:8081", // routed: the OS's choice
		"127.0.0.1":     "http://127.0.0.1:8081",
	}
	for remote, want := range cases {
		if got := a.BaseFor(net.ParseIP(remote)); got != want {
			t.Errorf("BaseFor(%s) = %s, want %s", remote, got, want)
		}
	}
	if got := (Advertiser{HTTPAddr: "", Addrs: lanAddrs}).BaseFor(net.ParseIP("192.168.50.9")); got != "http://192.168.50.177:8081" {
		t.Errorf("default port: %s", got)
	}
	if got := (Advertiser{PublicBase: "http://tv.lan:9000/", Addrs: lanAddrs}).BaseFor(net.ParseIP("10.8.0.7")); got != "http://tv.lan:9000" {
		t.Errorf("public base wins: %s", got)
	}
	if got := (Advertiser{HTTPAddr: "192.168.50.177:8082"}).BaseFor(net.ParseIP("10.8.0.7")); got != "http://192.168.50.177:8082" {
		t.Errorf("listen host: %s", got)
	}
}

// --- SSDP ------------------------------------------------------------------

func TestSSDPReply_HasAbsoluteLocationAndStableUSN(t *testing.T) {
	dev := Device{UUID: "3f2fbedc-eae3-432b-9996-e128ac4cce90", DeviceID: "87C77788", FriendlyName: "Maktaba"}
	base := Advertiser{HTTPAddr: ":8081", Addrs: lanAddrs}.BaseFor(net.ParseIP("192.168.50.20"))
	resp := buildSSDPResponseST(base, dev.UUID, ssdpST)
	if !strings.Contains(resp, "\r\nLOCATION: http://192.168.50.177:8081/device.xml\r\n") {
		t.Fatalf("LOCATION not absolute:\n%s", resp)
	}
	if strings.Contains(resp, "LOCATION: /") {
		t.Fatal("relative LOCATION")
	}
	xml := deviceXML(dev, base)
	if !strings.Contains(xml, "<UDN>uuid:"+dev.UUID+"</UDN>") ||
		!strings.Contains(resp, "\r\nUSN: uuid:"+dev.UUID+"::"+ssdpST+"\r\n") {
		t.Fatal("USN must carry device.xml's UDN")
	}
	if got := buildSSDPResponseST(base, dev.UUID, "uuid:"+dev.UUID); !strings.Contains(got, "\r\nUSN: uuid:"+dev.UUID+"\r\n") {
		t.Errorf("uuid target USN:\n%s", got)
	}
}

func TestMSearchTargets(t *testing.T) {
	msg := func(st string) string {
		return "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: " + st + "\r\n\r\n"
	}
	if got := msearchTargets(msg("ssdp:all"), "u"); len(got) != 3 {
		t.Errorf("ssdp:all: %v", got)
	}
	if got := msearchTargets(msg("upnp:rootdevice"), "u"); len(got) != 1 || got[0] != ssdpRootDevice {
		t.Errorf("rootdevice: %v", got)
	}
	if got := msearchTargets(msg(ssdpST), "u"); len(got) != 1 || got[0] != ssdpST {
		t.Errorf("device type: %v", got)
	}
	if got := msearchTargets(msg("uuid:u"), "u"); len(got) != 1 {
		t.Errorf("uuid: %v", got)
	}
	if got := msearchTargets(msg("urn:schemas-upnp-org:device:InternetGatewayDevice:1"), "u"); got != nil {
		t.Errorf("unrelated: %v", got)
	}
}

func loopbackUDP(t *testing.T) net.PacketConn {
	t.Helper()
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func readAll(t *testing.T, c net.PacketConn, n int) []string {
	t.Helper()
	var out []string
	buf := make([]byte, 2048)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for len(out) < n {
		k, _, err := c.ReadFrom(buf)
		if err != nil {
			t.Fatalf("read %d of %d: %v", len(out), n, err)
		}
		out = append(out, string(buf[:k]))
	}
	return out
}

type notifyLog struct {
	mu   sync.Mutex
	msgs []string
}

func (l *notifyLog) send(_ LocalIface, b []byte) error {
	l.mu.Lock()
	l.msgs = append(l.msgs, string(b))
	l.mu.Unlock()
	return nil
}

func (l *notifyLog) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.msgs {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

func TestSSDPResponder_LoopbackMSearchAndNotify(t *testing.T) {
	srv, client := loopbackUDP(t), loopbackUDP(t)
	defer client.Close()
	var log notifyLog
	r := &Responder{Conn: srv, DeviceUUID: "u-1",
		Advertiser: Advertiser{HTTPAddr: ":8081", Addrs: lanAddrs},
		Ifaces: func() []LocalIface {
			return []LocalIface{{Ifi: net.Interface{Index: 7, Name: "en0"}, IP: net.IPv4(192, 168, 50, 177)}}
		},
		Multicast: log.send}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	msearch := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: ssdp:all\r\n\r\n"
	if _, err := client.WriteTo([]byte(msearch), srv.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	for _, rep := range readAll(t, client, 3) {
		if !strings.Contains(rep, "\r\nLOCATION: http://127.0.0.1:8081/device.xml\r\n") {
			t.Errorf("reply LOCATION:\n%s", rep)
		}
	}
	if !waitTrue(func() bool { return log.count("NTS: ssdp:alive") == 3 }) {
		t.Fatalf("alive NOTIFYs: %v", log.msgs)
	}
	if log.count("LOCATION: http://192.168.50.177:8081/device.xml") != 3 {
		t.Errorf("NOTIFY LOCATION must be the interface's address: %v", log.msgs)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}
	if log.count("NTS: ssdp:byebye") != 3 {
		t.Errorf("byebye on shutdown: %v", log.msgs)
	}
	if _, err := srv.WriteTo([]byte("x"), client.LocalAddr()); err == nil {
		t.Error("socket still open after stop")
	}
}

// --- HDHomeRun UDP responder ---------------------------------------------

func TestDiscoverResponder_LoopbackRequestGetsReply(t *testing.T) {
	srv, client := loopbackUDP(t), loopbackUDP(t)
	defer client.Close()
	d := &DiscoverResponder{Conn: srv, Device: Device{DeviceID: "87C77788", TunerCount: 20},
		Advertiser: Advertiser{HTTPAddr: ":8081", Addrs: lanAddrs}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()
	if _, err := client.WriteTo(unhex(t, libhdhrRequestLegacy), srv.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	tags := replyTags(t, []byte(readAll(t, client, 1)[0]))
	if string(tags[hdhrTagBaseURL]) != "http://127.0.0.1:8081" ||
		string(tags[hdhrTagLineupURL]) != "http://127.0.0.1:8081/lineup.json" {
		t.Errorf("urls %q %q", tags[hdhrTagBaseURL], tags[hdhrTagLineupURL])
	}
}

// --- lifecycle: nothing listens while the tuner is off --------------------

func TestController_ResponderOnlyWhileEnabled(t *testing.T) {
	repo := &togglingRepo{dev: Device{UUID: "u-1", DeviceID: "87C77788", TunerCount: 4, Enabled: false}}
	var ssdpBinds, discBinds atomic.Int32
	var mu sync.Mutex
	var discConn net.PacketConn
	var log notifyLog
	c := &SSDPController{Repo: repo, HTTPAddr: ":8081",
		ListenSSDP: func() (net.PacketConn, error) {
			ssdpBinds.Add(1)
			return net.ListenPacket("udp4", "127.0.0.1:0")
		},
		ListenDiscover: func() (net.PacketConn, error) {
			discBinds.Add(1)
			conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
			mu.Lock()
			discConn = conn
			mu.Unlock()
			return conn, err
		},
		Ifaces:    func() []LocalIface { return nil },
		Multicast: log.send,
	}
	ctx := context.Background()
	defer c.Stop()

	if c.Sync(ctx) || c.Running() {
		t.Fatal("running while the tuner is disabled")
	}
	time.Sleep(50 * time.Millisecond)
	if ssdpBinds.Load() != 0 || discBinds.Load() != 0 {
		t.Fatal("a socket was opened while the tuner is disabled")
	}

	repo.set(func(d *Device) { d.Enabled = true })
	if !c.Sync(ctx) || !waitTrue(func() bool { return ssdpBinds.Load() == 1 && discBinds.Load() == 1 }) {
		t.Fatal("enable did not start both responders")
	}
	mu.Lock()
	conn := discConn
	mu.Unlock()
	client := loopbackUDP(t)
	defer client.Close()
	if _, err := client.WriteTo(unhex(t, libhdhrRequestCurrent), conn.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	tags := replyTags(t, []byte(readAll(t, client, 1)[0]))
	if binary.BigEndian.Uint32(tags[hdhrTagDeviceID]) != 0x87C77788 || tags[hdhrTagTunerCount][0] != 4 {
		t.Errorf("reply tags %v", tags)
	}

	repo.set(func(d *Device) { d.Enabled = false })
	if c.Sync(ctx) || c.Running() {
		t.Fatal("disable did not stop the responders")
	}
	if _, err := conn.WriteTo([]byte("x"), client.LocalAddr()); err == nil {
		t.Error("discover socket still open after disable")
	}
	binds := discBinds.Load()
	c.Sync(ctx)
	time.Sleep(50 * time.Millisecond)
	if discBinds.Load() != binds {
		t.Error("re-bound while disabled")
	}
}
