package hdhr

// DiscoverResponse is the /discover.json payload (AC2). Field names match
// what Plex/Jellyfin/Emby expect from a real HDHomeRun.
type DiscoverResponse struct {
	FriendlyName    string `json:"FriendlyName"`
	Manufacturer    string `json:"Manufacturer"`
	ModelNumber     string `json:"ModelNumber"`
	FirmwareName    string `json:"FirmwareName"`
	FirmwareVersion string `json:"FirmwareVersion"`
	DeviceID        string `json:"DeviceID"`
	DeviceAuth      string `json:"DeviceAuth"`
	TunerCount      int    `json:"TunerCount"`
	BaseURL         string `json:"BaseURL"`
	LineupURL       string `json:"LineupURL"`
}

// Fixed identity values media servers accept as a clear-QAM/ATSC tuner.
const (
	manufacturer    = "Maktaba"
	modelNumber     = "HDTC-2US"
	firmwareName    = "maktaba_atsc"
	firmwareVersion = "20260601"
)

// buildDiscover builds the /discover.json body from the device row and
// the request-derived base URL (D4 — works over LAN IP and the relay
// host alike). deviceAuth is the scoped token embedded in the URLs.
func buildDiscover(dev Device, baseURL, deviceAuth string) DiscoverResponse {
	return DiscoverResponse{
		FriendlyName:    dev.FriendlyName,
		Manufacturer:    manufacturer,
		ModelNumber:     modelNumber,
		FirmwareName:    firmwareName,
		FirmwareVersion: firmwareVersion,
		DeviceID:        dev.DeviceID,
		DeviceAuth:      deviceAuth,
		TunerCount:      dev.TunerCount,
		BaseURL:         baseURL,
		LineupURL:       baseURL + "/lineup.json",
	}
}

// deviceXML renders the UPnP device description referenced by the SSDP
// LOCATION header. The UDN carries the persisted device UUID so Plex
// binds its DVR to a stable identity (EC1).
func deviceXML(dev Device, baseURL string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<root xmlns="urn:schemas-upnp-org:device-1-0">` + "\n" +
		`  <specVersion><major>1</major><minor>0</minor></specVersion>` + "\n" +
		`  <URLBase>` + xmlEscape(baseURL) + `</URLBase>` + "\n" +
		`  <device>` + "\n" +
		`    <deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType>` + "\n" +
		`    <friendlyName>` + xmlEscape(dev.FriendlyName) + `</friendlyName>` + "\n" +
		`    <manufacturer>` + manufacturer + `</manufacturer>` + "\n" +
		`    <modelName>` + modelNumber + `</modelName>` + "\n" +
		`    <modelNumber>` + modelNumber + `</modelNumber>` + "\n" +
		`    <serialNumber>` + xmlEscape(dev.DeviceID) + `</serialNumber>` + "\n" +
		`    <UDN>uuid:` + xmlEscape(dev.UUID) + `</UDN>` + "\n" +
		`  </device>` + "\n" +
		`</root>` + "\n"
}

func xmlEscape(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch r {
		case '&':
			out = append(out, "&amp;"...)
		case '<':
			out = append(out, "&lt;"...)
		case '>':
			out = append(out, "&gt;"...)
		case '"':
			out = append(out, "&quot;"...)
		default:
			out = append(out, string(r)...)
		}
	}
	return string(out)
}
