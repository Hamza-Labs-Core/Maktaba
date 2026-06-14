package hdhr

import "strconv"

// LineupEntry is one channel in /lineup.json (AC3). GuideNumber joins to
// the XMLTV guide; URL is this device's /auto/v{number} tuner endpoint.
type LineupEntry struct {
	GuideNumber string `json:"GuideNumber"`
	GuideName   string `json:"GuideName"`
	URL         string `json:"URL"`
}

// LineupStatus is /lineup_status.json (AC4) — an idle, scannable status.
type LineupStatus struct {
	ScanInProgress int      `json:"ScanInProgress"`
	ScanPossible   int      `json:"ScanPossible"`
	Source         string   `json:"Source"`
	SourceList     []string `json:"SourceList"`
}

// buildLineup renders the lineup entries for the given channels, pointing
// each at this device's /auto/v{number} endpoint under baseURL.
func buildLineup(channels []LineupChannel, baseURL string) []LineupEntry {
	out := make([]LineupEntry, 0, len(channels))
	for _, c := range channels {
		num := strconv.Itoa(c.Number)
		out = append(out, LineupEntry{
			GuideNumber: num,
			GuideName:   c.Name,
			URL:         baseURL + "/auto/v" + num,
		})
	}
	return out
}

// idleStatus is the steady-state lineup status. Maktaba's lineup is a
// query, not a hardware scan, so a scan is "possible" but never in
// progress.
func idleStatus() LineupStatus {
	return LineupStatus{
		ScanInProgress: 0,
		ScanPossible:   1,
		Source:         "Cable",
		SourceList:     []string{"Cable"},
	}
}
