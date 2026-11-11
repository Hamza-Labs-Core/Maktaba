package parental

import "testing"

func ip(n int) *int { return &n }

func check(t *testing.T, name string, got Result, tier *int, src Source) {
	t.Helper()
	switch {
	case (got.Tier == nil) != (tier == nil):
		t.Errorf("%s: tier %v, want %v", name, got.Tier, tier)
	case tier != nil && *got.Tier != *tier:
		t.Errorf("%s: tier %d, want %d", name, *got.Tier, *tier)
	}
	if got.Source != src {
		t.Errorf("%s: source %q, want %q", name, got.Source, src)
	}
}

// T03: the precedence cases of the story's acceptance criteria.
func TestResolvePrecedence(t *testing.T) {
	cases := []struct {
		name string
		in   Video
		tier *int
		src  Source
	}{
		{"a kept Plex rating", Video{PlexRatings: []string{"gb/15"}}, ip(16), SourcePlex},
		{"in a collection an admin set to 18", Video{PlexRatings: []string{"gb/15"}, ContainerTiers: []int{18}}, ip(18), SourceAdminContainer},
		{"then the admin sets the video to 13", Video{PlexRatings: []string{"gb/15"}, ContainerTiers: []int{18}, AdminTier: ip(13)}, ip(13), SourceAdminVideo},
		{"a container looser than the rating still wins", Video{PlexRatings: []string{"R"}, ContainerTiers: []int{9}}, ip(9), SourceAdminContainer},
		{"two collections, 9 and 16", Video{ContainerTiers: []int{9, 16}}, ip(16), SourceAdminContainer},
		{"a container set to All ages", Video{ContainerTiers: []int{0}, LibraryDefault: ip(18)}, ip(0), SourceAdminContainer},
		{"YouTube's age limit of 18", Video{YouTubeAgeLimit: ip(18)}, ip(18), SourceYouTube},
		{"YouTube's age limit of 0 is no rating", Video{YouTubeAgeLimit: ip(0), LibraryDefault: ip(9)}, ip(9), SourceLibraryDefault},
		{"a guide rating", Video{GuideRatings: []string{"TV-14"}}, ip(16), SourceGuide},
		{"the strictest metadata rating", Video{PlexRatings: []string{"PG"}, GuideRatings: []string{"TV-MA"}, YouTubeAgeLimit: ip(13)}, ip(18), SourceGuide},
		{"two Plex rows", Video{PlexRatings: []string{"PG", "de/16"}}, ip(16), SourcePlex},
		{"a tie keeps the first source", Video{PlexRatings: []string{"R"}, PluginRating: "NC-17"}, ip(18), SourcePlex},
		{"a plugin's rating", Video{PluginRating: "au/M"}, ip(16), SourcePlugin},
		{"a library default of 0, no rating", Video{LibraryDefault: ip(0)}, ip(0), SourceLibraryDefault},
		{"an unknown rating falls to the library default", Video{PlexRatings: []string{"Approved"}, LibraryDefault: ip(13)}, ip(13), SourceLibraryDefault},
		{"a rating wins over the library default", Video{PlexRatings: []string{"G"}, LibraryDefault: ip(16)}, ip(0), SourcePlex},
		{"nothing", Video{}, nil, SourceNone},
		{"an unknown rating, no default", Video{PluginRating: "XYZ"}, nil, SourceNone},
		{"the admin clears the video: back down the order", Video{AdminTier: nil, ContainerTiers: []int{16}, PlexRatings: []string{"G"}}, ip(16), SourceAdminContainer},
		{"a tier off the scale is not a tier", Video{AdminTier: ip(12), ContainerTiers: []int{7}, LibraryDefault: ip(9)}, ip(9), SourceLibraryDefault},
	}
	for _, c := range cases {
		check(t, c.name, Resolve(c.in), c.tier, c.src)
	}
}

// T05: plugins never loosen.
func TestResolvePluginsNeverLoosen(t *testing.T) {
	check(t, "plugin G against Plex R", Resolve(Video{PluginRating: "G", PlexRatings: []string{"R"}}), ip(18), SourcePlex)
	check(t, "plugin R18+ against the admin's 9", Resolve(Video{PluginRating: "au/R18+", AdminTier: ip(9)}), ip(9), SourceAdminVideo)
	check(t, "plugin R18+ against a container's 9", Resolve(Video{PluginRating: "au/R18+", ContainerTiers: []int{9}}), ip(9), SourceAdminContainer)
	check(t, "a plugin may tighten a rating", Resolve(Video{PluginRating: "au/R18+", PlexRatings: []string{"G"}}), ip(18), SourcePlugin)
}
