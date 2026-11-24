package updates

import (
	"encoding/json"
	"strings"
	"testing"
)

// The three small signed files a home downloads for each release (checksums.txt, its
// Sigstore bundle, release.json), and the component index beside them, fit smallFileLimit
// at their real sizes (GitHub's own count, in the recorded page) doubled, in under
// FeedHeadroom of it.
func TestRealSmallFilesHeadroom(t *testing.T) {
	t.Parallel()
	rels := realReleases(t)
	var newest struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(rels[0], &newest); err != nil {
		t.Fatal(err)
	}
	small := map[string]bool{ChecksumsFile: true, BundleFile: true, ManifestFile: true,
		"components-" + strings.TrimPrefix(newest.Tag, "v") + ".json": true}
	seen := 0
	for _, a := range newest.Assets {
		if !small[a.Name] {
			continue
		}
		seen++
		if grown := AssetGrowth * float64(a.Size); grown > FeedHeadroom*smallFileLimit {
			t.Errorf("%s's %s is %d bytes: %.1fx that is over %.0f%% of smallFileLimit %d",
				newest.Tag, a.Name, a.Size, AssetGrowth, 100*FeedHeadroom, smallFileLimit)
		}
	}
	if seen != len(small) {
		t.Fatalf("%s lists %d of the %d small signed files", newest.Tag, seen, len(small))
	}
}
