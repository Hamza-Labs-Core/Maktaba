package hdhr

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
)

// The SiliconDust HDHomeRun discovery protocol, the one Plex's "scan for
// devices" and the HDHomeRun apps broadcast on UDP 65001. The layout follows
// libhdhomerun (github.com/Silicondust/libhdhomerun, LGPL):
//
//   - hdhomerun_pkt.h, the comment above HDHOMERUN_DISCOVER_UDP_PORT: a frame
//     is uint16 packet type, uint16 payload length, the payload, then a
//     32-bit "Ethernet style" CRC. Everything is big-endian except the CRC,
//     which is little-endian.
//   - hdhomerun_pkt.c, hdhomerun_pkt_calc_crc: the reflected CRC-32 with
//     polynomial 0xEDB88320, init 0xFFFFFFFF and final xor 0xFFFFFFFF, i.e.
//     IEEE CRC-32 (hash/crc32.ChecksumIEEE), over the 4-byte header and the
//     payload; hdhomerun_pkt_seal_frame writes it low byte first.
//   - The payload is tag-length-value: a uint8 tag, a varlen length (one byte
//     when <= 127, else two bytes: (len & 0x7F) | 0x80, then len >> 7; see
//     hdhomerun_pkt_write_var_length / hdhomerun_pkt_read_var_length) and the
//     value.
//   - hdhomerun_discover.c, hdhomerun_discover_send_request: a request carries
//     HDHOMERUN_TAG_DEVICE_TYPE (one uint32) or HDHOMERUN_TAG_MULTI_TYPE
//     (several), and HDHOMERUN_TAG_DEVICE_ID unless it is the wildcard (older
//     releases always sent the wildcard id).
//   - hdhomerun_discover.c, hdhomerun_discover_recv_internal: a reply's
//     DEVICE_TYPE and DEVICE_ID must be 4 bytes, TUNER_COUNT 1 byte; strings
//     (BASE_URL, LINEUP_URL, DEVICE_AUTH_STR) are read for their TLV length
//     with no NUL needed; unknown tags are skipped.

// HDHRDiscoverPort is HDHOMERUN_DISCOVER_UDP_PORT (hdhomerun_pkt.h).
const HDHRDiscoverPort = 65001

// Packet types and tags (hdhomerun_pkt.h).
const (
	hdhrTypeDiscoverReq uint16 = 0x0002 // HDHOMERUN_TYPE_DISCOVER_REQ
	hdhrTypeDiscoverRpy uint16 = 0x0003 // HDHOMERUN_TYPE_DISCOVER_RPY

	hdhrTagDeviceType    byte = 0x01 // HDHOMERUN_TAG_DEVICE_TYPE
	hdhrTagDeviceID      byte = 0x02 // HDHOMERUN_TAG_DEVICE_ID
	hdhrTagTunerCount    byte = 0x10 // HDHOMERUN_TAG_TUNER_COUNT
	hdhrTagLineupURL     byte = 0x27 // HDHOMERUN_TAG_LINEUP_URL
	hdhrTagBaseURL       byte = 0x2A // HDHOMERUN_TAG_BASE_URL
	hdhrTagDeviceAuthStr byte = 0x2B // HDHOMERUN_TAG_DEVICE_AUTH_STR
	hdhrTagMultiType     byte = 0x2D // HDHOMERUN_TAG_MULTI_TYPE

	hdhrDeviceTypeWildcard uint32 = 0xFFFFFFFF // HDHOMERUN_DEVICE_TYPE_WILDCARD
	hdhrDeviceTypeTuner    uint32 = 0x00000001 // HDHOMERUN_DEVICE_TYPE_TUNER
	hdhrDeviceIDWildcard   uint32 = 0xFFFFFFFF // HDHOMERUN_DEVICE_ID_WILDCARD
)

// maxVarLen is the largest length the two-byte varlen can carry.
const maxVarLen = 0x7FFF

var (
	errShortPacket = errors.New("hdhr: short packet")
	errBadCRC      = errors.New("hdhr: bad CRC")
)

// encodeHDHRPacket frames payload like hdhomerun_pkt_seal_frame.
func encodeHDHRPacket(typ uint16, payload []byte) []byte {
	out := make([]byte, 4, 4+len(payload)+4)
	binary.BigEndian.PutUint16(out[0:2], typ)
	binary.BigEndian.PutUint16(out[2:4], uint16(len(payload)))
	out = append(out, payload...)
	return binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

// decodeHDHRPacket opens a frame like hdhomerun_pkt_open_frame: it checks the
// length and the CRC and returns the type and the payload.
func decodeHDHRPacket(b []byte) (uint16, []byte, error) {
	if len(b) < 8 {
		return 0, nil, errShortPacket
	}
	typ := binary.BigEndian.Uint16(b[0:2])
	n := int(binary.BigEndian.Uint16(b[2:4]))
	if len(b) < 4+n+4 {
		return 0, nil, errShortPacket
	}
	if crc32.ChecksumIEEE(b[:4+n]) != binary.LittleEndian.Uint32(b[4+n:]) {
		return 0, nil, errBadCRC
	}
	return typ, b[4 : 4+n], nil
}

// hdhrTLV is one tag-length-value item.
type hdhrTLV struct {
	Tag   byte
	Value []byte
}

// appendTLV writes one TLV (hdhomerun_pkt_write_var_length); values longer
// than the varlen can say are cut.
func appendTLV(b []byte, tag byte, value []byte) []byte {
	if len(value) > maxVarLen {
		value = value[:maxVarLen]
	}
	b = append(b, tag)
	if n := len(value); n <= 127 {
		b = append(b, byte(n))
	} else {
		b = append(b, byte(n&0x7F)|0x80, byte(n>>7))
	}
	return append(b, value...)
}

func appendTLVU32(b []byte, tag byte, v uint32) []byte {
	return appendTLV(b, tag, binary.BigEndian.AppendUint32(nil, v))
}

// parseTLVs reads a payload (hdhomerun_pkt_read_tlv); a truncated item ends
// the list, as in libhdhomerun.
func parseTLVs(p []byte) []hdhrTLV {
	var out []hdhrTLV
	for len(p) >= 2 {
		tag := p[0]
		n := int(p[1])
		p = p[2:]
		if n&0x80 != 0 {
			if len(p) < 1 {
				break
			}
			n = n&0x7F | int(p[0])<<7
			p = p[1:]
		}
		if n > len(p) {
			break
		}
		out = append(out, hdhrTLV{Tag: tag, Value: p[:n]})
		p = p[n:]
	}
	return out
}

// hdhrDiscoverInfo is what a discover reply advertises.
type hdhrDiscoverInfo struct {
	DeviceID   uint32
	TunerCount int
	BaseURL    string
	LineupURL  string
	DeviceAuth string
}

// discoverRequestMatches reports whether a DISCOVER_REQ payload asks for us:
// a tuner (or wildcard) device type, and our id or the wildcard id. A request
// with no type tag is taken as a wildcard.
func discoverRequestMatches(payload []byte, deviceID uint32) bool {
	typeOK, sawType := false, false
	for _, t := range parseTLVs(payload) {
		switch t.Tag {
		case hdhrTagDeviceType, hdhrTagMultiType:
			if t.Tag == hdhrTagDeviceType && len(t.Value) != 4 {
				continue
			}
			sawType = true
			for v := t.Value; len(v) >= 4; v = v[4:] {
				if dt := binary.BigEndian.Uint32(v); dt == hdhrDeviceTypeTuner || dt == hdhrDeviceTypeWildcard {
					typeOK = true
				}
			}
		case hdhrTagDeviceID:
			if len(t.Value) != 4 {
				continue
			}
			if id := binary.BigEndian.Uint32(t.Value); id != hdhrDeviceIDWildcard && id != deviceID {
				return false
			}
		}
	}
	return typeOK || !sawType
}

// buildDiscoverReply is the DISCOVER_RPY frame a real tuner sends, with the
// tags hdhomerun_discover_recv_internal reads.
func buildDiscoverReply(info hdhrDiscoverInfo) []byte {
	var p []byte
	p = appendTLVU32(p, hdhrTagDeviceType, hdhrDeviceTypeTuner)
	p = appendTLVU32(p, hdhrTagDeviceID, info.DeviceID)
	tc := info.TunerCount
	if tc < 1 {
		tc = 1
	} else if tc > 255 {
		tc = 255
	}
	p = appendTLV(p, hdhrTagTunerCount, []byte{byte(tc)})
	if info.DeviceAuth != "" {
		p = appendTLV(p, hdhrTagDeviceAuthStr, []byte(info.DeviceAuth))
	}
	p = appendTLV(p, hdhrTagBaseURL, []byte(info.BaseURL))
	p = appendTLV(p, hdhrTagLineupURL, []byte(info.LineupURL))
	return encodeHDHRPacket(hdhrTypeDiscoverRpy, p)
}

// handleDiscoverPacket answers one datagram: the reply frame, or nil when the
// datagram is not a valid discover request for this device.
func handleDiscoverPacket(b []byte, info hdhrDiscoverInfo) []byte {
	typ, payload, err := decodeHDHRPacket(b)
	if err != nil || typ != hdhrTypeDiscoverReq || !discoverRequestMatches(payload, info.DeviceID) {
		return nil
	}
	return buildDiscoverReply(info)
}

// deviceIDChecksumTable is the lookup_table of
// hdhomerun_discover_validate_device_id (hdhomerun_discover.c).
var deviceIDChecksumTable = [16]byte{0xA, 0x5, 0xF, 0x6, 0x7, 0xC, 0x1, 0xB, 0x9, 0x2, 0x8, 0xD, 0x4, 0x3, 0xE, 0x0}

// deviceIDChecksum xors the nibbles like hdhomerun_discover_validate_device_id:
// the odd nibbles (from the top) through the table, the even ones as is.
func deviceIDChecksum(id uint32) byte {
	var c byte
	for shift := 28; shift >= 0; shift -= 8 {
		c ^= deviceIDChecksumTable[(id>>uint(shift))&0x0F]
		c ^= byte(id>>uint(shift-4)) & 0x0F
	}
	return c
}

// ValidDeviceID reports whether id passes libhdhomerun's check digit
// (hdhomerun_discover_validate_device_id); HDHomeRun clients drop ids that
// fail it.
func ValidDeviceID(id uint32) bool { return deviceIDChecksum(id) == 0 }

// WithCheckDigit sets id's last nibble so it passes ValidDeviceID. The last
// nibble enters the checksum unmapped, so it is the xor of the other seven.
func WithCheckDigit(id uint32) uint32 {
	id &^= 0x0F
	return id | uint32(deviceIDChecksum(id))
}

// ParseDeviceID parses an 8-hex-digit DeviceID.
func ParseDeviceID(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if len(s) != 8 {
		return 0, fmt.Errorf("hdhr: device id %q is not 8 hex digits", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("hdhr: device id %q: %w", s, err)
	}
	return uint32(v), nil
}

// FormatDeviceID is the 8 upper-case hex digits of id.
func FormatDeviceID(id uint32) string { return fmt.Sprintf("%08X", id) }

// RepairDeviceID returns s with a valid check digit (only the last digit
// changes, so the id stays recognisable), and whether it changed. A string
// that is not an 8-hex-digit id is returned as is.
func RepairDeviceID(s string) (string, bool) {
	id, err := ParseDeviceID(s)
	if err != nil || ValidDeviceID(id) {
		return s, false
	}
	return FormatDeviceID(WithCheckDigit(id)), true
}
