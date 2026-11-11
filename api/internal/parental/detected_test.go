package parental

import "testing"

// Story 57.17 T03: the detected tier against metadata and admin tiers. The loader gives
// Resolve a DetectedTier only at or above the confidence line (recompute_detected_pg_test.go
// has the line itself), so "below the line" is a nil here.
func TestResolveDetected(t *testing.T) {
	cases := []struct {
		name string
		in   Video
		tier *int
		src  Source
	}{
		{"metadata 13 and detected 16", Video{PlexRatings: []string{"PG-13"}, DetectedTier: ip(16)}, ip(16), SourceDetected},
		{"metadata 16 and detected 9", Video{PlexRatings: []string{"gb/15"}, DetectedTier: ip(9)}, ip(16), SourcePlex},
		{"the admin sets 9 on the video", Video{AdminTier: ip(9), DetectedTier: ip(18)}, ip(9), SourceAdminVideo},
		{"the admin's container tier of 9", Video{ContainerTiers: []int{9}, DetectedTier: ip(18)}, ip(9), SourceAdminContainer},
		{"an unrated video above the line", Video{DetectedTier: ip(16)}, ip(16), SourceDetected},
		{"an unrated video below the line", Video{DetectedTier: nil}, nil, SourceNone},
		{"detected All ages is a tier", Video{DetectedTier: ip(0)}, ip(0), SourceDetected},
		{"a tie keeps the metadata source", Video{GuideRatings: []string{"TV-14"}, DetectedTier: ip(16)}, ip(16), SourceGuide},
		{"a tie with a plugin keeps the plugin", Video{PluginRating: "au/M", DetectedTier: ip(16)}, ip(16), SourcePlugin},
		{"stricter than a plugin", Video{PluginRating: "G", DetectedTier: ip(13)}, ip(13), SourceDetected},
		{"a tier off the scale is not a tier", Video{DetectedTier: ip(12)}, nil, SourceNone},
		// alone, a detected tier never brings a video under the library's default
		{"under the library default", Video{DetectedTier: ip(0), LibraryDefault: ip(13)}, ip(13), SourceLibraryDefault},
		{"over the library default", Video{DetectedTier: ip(18), LibraryDefault: ip(13)}, ip(18), SourceDetected},
		{"equal to the library default", Video{DetectedTier: ip(13), LibraryDefault: ip(13)}, ip(13), SourceDetected},
		{"with a plugin, both under the default", Video{PluginRating: "G", DetectedTier: ip(9), LibraryDefault: ip(16)}, ip(16), SourceLibraryDefault},
		// beside a trusted rating the default plays no part, as for a plugin
		{"a Plex rating, detected stricter, default stricter still", Video{PlexRatings: []string{"G"}, DetectedTier: ip(13), LibraryDefault: ip(18)}, ip(13), SourceDetected},
	}
	for _, c := range cases {
		check(t, c.name, Resolve(c.in), c.tier, c.src)
	}
}

func TestParseDetectLine(t *testing.T) {
	for raw, want := range map[string]float64{
		`0.8`:    0.8,
		`0.5`:    0.5,
		`0`:      0,
		`1`:      1,
		`1.5`:    DefaultDetectLine,
		`-0.1`:   DefaultDetectLine,
		`"0.3"`:  DefaultDetectLine,
		`null`:   DefaultDetectLine,
		`{}`:     DefaultDetectLine,
		`broken`: DefaultDetectLine,
		``:       DefaultDetectLine,
	} {
		if got := parseDetectLine([]byte(raw)); got != want {
			t.Errorf("parseDetectLine(%q) = %v, want %v", raw, got, want)
		}
	}
}
