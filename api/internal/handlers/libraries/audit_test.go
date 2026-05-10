package libraries

import (
	"strings"
	"testing"
)

func TestEventConstants_VocabularyMatchesPython(t *testing.T) {
	// Sanity that the Go constants match the Python sibling exactly.
	want := []string{
		"scan-triggered",
		"settings-changed",
		"video-purged",
		"library-deleted",
		"speaker-merged",
		"file-purge-results",
		"duplicate-detected",
		"runtime-root-overlap",
		"path-out-of-root",
		"topic-recluster",
	}
	got := []string{
		EventScanTriggered,
		EventSettingsChanged,
		EventVideoPurged,
		EventLibraryDeleted,
		EventSpeakerMerged,
		EventFilePurgeResults,
		EventDuplicateDetected,
		EventRuntimeRootOverlap,
		EventPathOutOfRoot,
		EventTopicRecluster,
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("event vocabulary drift:\nwant=%v\ngot=%v", want, got)
	}
}

func TestAuditPayloadMaxBytes_PinnedTo8KiB(t *testing.T) {
	// AC EC: payload length cap.
	if AuditPayloadMaxBytes != 8*1024 {
		t.Errorf("payload cap drifted: %d", AuditPayloadMaxBytes)
	}
}
