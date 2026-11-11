package parental

// Source says where a video's tier came from (videos.age_tier_source).
type Source string

const (
	SourceAdminVideo     Source = "admin_video"
	SourceAdminContainer Source = "admin_container"
	SourcePlex           Source = "plex"
	SourceGuide          Source = "guide"
	SourceYouTube        Source = "youtube"
	SourcePlugin         Source = "plugin"
	// SourceDetected is the tier the home's own model worked out from the transcript
	// (video_content_signals, story 57.17).
	SourceDetected       Source = "detected"
	SourceLibraryDefault Source = "library_default"
	// SourceNone is an unrated video's source (stored as NULL).
	SourceNone Source = ""
)

// Video is what decides one video's tier.
type Video struct {
	// AdminTier is the tier an admin set on the video itself (videos.age_tier_admin).
	AdminTier *int
	// ContainerTiers are the tiers an admin set on what contains the video: its series,
	// its collections, its YouTube sources.
	ContainerTiers []int
	// PlexRatings are the kept Plex contentRating strings of the video's plex_items rows.
	PlexRatings []string
	// GuideRatings are the ratings of the video's guide programmes (title_snapshot.rating).
	GuideRatings []string
	// YouTubeAgeLimit is yt-dlp's age_limit of the video's YouTube match (nil: none kept).
	YouTubeAgeLimit *int
	// PluginRating is a plugin's content_rating (videos.metadata.content_rating).
	PluginRating string
	// DetectedTier is the tier the home's model detected (video_content_signals), nil
	// when there is none that counts: the loader leaves out a signal under the
	// confidence line and one an admin has reviewed.
	DetectedTier *int
	// LibraryDefault is the tier an admin gave the video's library (nil: none).
	LibraryDefault *int
}

// Result is a video's tier and where it came from. Tier is nil for an unrated video.
type Result struct {
	Tier   *int
	Source Source
}

// Resolve is the rule, first match wins: the admin's tier on the video; the strictest
// admin tier on a container; the strictest metadata rating (Plex, the guide, YouTube's
// age limit, a plugin, the detected tier); the library's default; unrated. A plugin's
// rating and a detected tier are metadata sources, so neither loosens an admin's tier or
// a stricter rating; and when they are the only ratings, they never bring the video
// under the library's default (the stricter of the two is the tier).
func Resolve(v Video) Result { return Default().Resolve(v) }

// Resolve is Resolve with t's tables.
func (t *Table) Resolve(v Video) Result {
	if v.AdminTier != nil && ValidTier(*v.AdminTier) {
		return rated(*v.AdminTier, SourceAdminVideo)
	}
	strictest, found := 0, false
	for _, c := range v.ContainerTiers {
		if ValidTier(c) && (!found || c > strictest) {
			strictest, found = c, true
		}
	}
	if found {
		return rated(strictest, SourceAdminContainer)
	}
	var src Source
	take := func(tier int, s Source) {
		if !found || tier > strictest {
			strictest, found, src = tier, true, s
		}
	}
	for _, r := range v.PlexRatings {
		if tier, ok := t.Content(r); ok {
			take(tier, SourcePlex)
		}
	}
	for _, r := range v.GuideRatings {
		if tier, ok := t.Content(r); ok {
			take(tier, SourceGuide)
		}
	}
	// yt-dlp's 0 means no limit is known, not a rating
	if v.YouTubeAgeLimit != nil && *v.YouTubeAgeLimit > 0 {
		take(ContentTier(*v.YouTubeAgeLimit), SourceYouTube)
	}
	trusted := found
	if tier, ok := t.Content(v.PluginRating); ok {
		take(tier, SourcePlugin)
	}
	if v.DetectedTier != nil && ValidTier(*v.DetectedTier) {
		take(*v.DetectedTier, SourceDetected)
	}
	hasDefault := v.LibraryDefault != nil && ValidTier(*v.LibraryDefault)
	// A plugin's rating and a detected tier can only tighten. Beside a Plex, guide or
	// YouTube rating each counts when it is the strictest; alone, neither brings a
	// video under the default an admin gave the library.
	if found && !trusted && hasDefault && *v.LibraryDefault > strictest {
		return rated(*v.LibraryDefault, SourceLibraryDefault)
	}
	if found {
		return rated(strictest, src)
	}
	if hasDefault {
		return rated(*v.LibraryDefault, SourceLibraryDefault)
	}
	return Result{}
}

func rated(tier int, s Source) Result { return Result{Tier: &tier, Source: s} }
