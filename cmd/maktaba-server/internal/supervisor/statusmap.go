package supervisor

// The status groups of /status.json (Epic 53 story 53.03): user-level parts, each a set
// of Epic 51 catalog ids. The catalog has no status field (its loaders refuse unknown
// fields), so the table lives here, keyed by catalog id; statusmap_catalog_test.go
// (story 53.18) fails when a process component of the catalog is in no group and not
// listed in NoStatusGroup with a reason.

// StatusGroup is one key of /status.json's components.
type StatusGroup struct {
	Name string
	// Components are the catalog ids whose processes make the group's state.
	Components []string
	// FeaturesDown are the words a group that is down puts in features_down: the
	// catalog's features words (live_tv, transcripts, semantic_search) and the status
	// words that have no switch of their own (playback, new_media, thumbnails,
	// log_search, all).
	FeaturesDown []string
	// Banner is the banner key a group that is not well raises ("": none for viewers).
	Banner string
}

// StatusGroups, in the order a banner is chosen (the first that is not well).
var StatusGroups = []StatusGroup{
	{Name: "database", Components: []string{"core.postgres"}, FeaturesDown: []string{"all"}, Banner: "database_down"},
	{Name: "api", Components: []string{"core.api"}, FeaturesDown: []string{"all"}, Banner: "api_down"},
	{Name: "streaming", Components: []string{"core.streaming", "livetv.slots"},
		FeaturesDown: []string{"playback", "live_tv"}, Banner: "streaming_down"},
	{Name: "processing", Components: []string{"core.pipeline", "transcription"},
		FeaturesDown: []string{"new_media", "transcripts", "thumbnails"}, Banner: "processing_down"},
	{Name: "search", Components: []string{"search.semantic"}, FeaturesDown: []string{"semantic_search"}, Banner: "search_down"},
	{Name: "logs", Components: []string{"logs.index"}, FeaturesDown: []string{"log_search"}},
}

// NoStatusGroup are the process components no status group shows, and why.
var NoStatusGroup = map[string]string{
	"llm":       "the language model server has no viewer feature of its own: the stages that use it wait on it",
	"core.node": "a full node's copy of the home's database: the Machines page shows it",
	"cloud.link": "remote access through Maktaba Cloud: the cloud's status (Epic 50.11) says it, " +
		"and the home stays usable without it",
	"core.plugin_runners": "Maktaba's own plugin runners (Epic 54, story 54.23): a machine whose runner is down " +
		"runs those stages in the worker, as before, so no viewer feature stops",
	"plugins.host": "the third-party plugins' host (Epic 54, story 54.09 §5): no viewer feature stops when " +
		"it is down (what plugins wrote stays); the admins' Plugins page says it",
}

// groupOf is the status group of a catalog id ("" when none).
func groupOf(id string) string {
	for _, g := range StatusGroups {
		for _, c := range g.Components {
			if c == id {
				return g.Name
			}
		}
	}
	return ""
}
