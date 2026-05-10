package graphql

import (
	"strings"
	"testing"
)

// TestSchema_RestParity is the AC-2 parity smoke check. We don't have
// a chi-route-table → schema diff here yet (that lives in a future
// integration test); but we do assert every Story-required type and
// every top-level Query/Mutation field exists in the SDL by string
// match. A future change that drops a field will fail this test.
func TestSchema_RestParity(t *testing.T) {
	requiredTypes := []string{
		"type Library", "type Video", "type MediaInfo", "type AudioTrack",
		"type Transcript", "type Segment", "type Word", "type Subtitle",
		"type Chapter", "type Tag", "type Collection", "type Speaker",
		"type Job", "type StreamingSession", "type User", "type PlaybackState",
		"type SearchResult", "type SearchHit", "type SearchMatch",
		"type Recommendation", "type Device",
	}
	for _, ty := range requiredTypes {
		if !strings.Contains(Schema, ty) {
			t.Errorf("missing %q in schema", ty)
		}
	}
	requiredFields := []string{
		"libraries:", "library(id", "videos(", "video(id",
		"search(", "queueStats:", "collections:", "tags:",
		"settings:", "recommendations(", "devices:",
		"createLibrary(", "patchLibrary(", "deleteLibrary(",
		"openStreamSession(", "postProgress(",
		"pauseJob(", "registerDevice(", "mergeSpeakers(",
		"jobUpdates:", "libraryEvents(", "playbackUpdates(",
	}
	for _, f := range requiredFields {
		if !strings.Contains(Schema, f) {
			t.Errorf("missing %q field", f)
		}
	}
}
