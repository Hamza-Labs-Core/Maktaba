package collections

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSmartQuery_DefaultsLibraryID(t *testing.T) {
	f, err := parseSmartQuery(json.RawMessage(`{"language":["ar"]}`), "lib-1")
	if err != nil {
		t.Fatal(err)
	}
	if f.LibraryID != "lib-1" {
		t.Errorf("library_id default lost: %+v", f)
	}
	if len(f.Languages) != 1 || f.Languages[0] != "ar" {
		t.Errorf("languages parse failed: %+v", f)
	}
}

func TestParseSmartQuery_HandlesEmpty(t *testing.T) {
	f, err := parseSmartQuery(nil, "lib-1")
	if err != nil {
		t.Fatal(err)
	}
	if f.LibraryID != "lib-1" {
		t.Errorf("expected lib-1, got %v", f.LibraryID)
	}
}

func TestParseSmartQuery_RejectsBadJSON(t *testing.T) {
	_, err := parseSmartQuery(json.RawMessage(`{not json`), "lib-1")
	if err == nil {
		t.Error("expected error on malformed JSON")
	}
}

func TestBuildSmartSQL_FilterClausesAreParameterised(t *testing.T) {
	f := SmartFilter{
		LibraryID:    "lib-1",
		Languages:    []string{"ar", "en"},
		ContentTypes: []string{"lecture"},
		States:       []string{"ready"},
		Tags:         []string{"tafsir"},
	}
	q, args := buildSmartSQL(f, 0, 50)
	// Should contain WHERE clauses for each filter.
	for _, want := range []string{"library_id =", "detected_language =", "content_type =", "state =", "video_tags"} {
		if !strings.Contains(q, want) {
			t.Errorf("expected %q in SQL: %s", want, q)
		}
	}
	// Args length should match filter count: 1 lib + 1 langs + 1 ct + 1 state + 1 tags = 5.
	if len(args) != 5 {
		t.Errorf("expected 5 args, got %d", len(args))
	}
}

func TestBuildSmartSQL_OrderByTitle(t *testing.T) {
	q, _ := buildSmartSQL(SmartFilter{OrderBy: "title"}, 0, 10)
	if !strings.Contains(q, "ORDER BY title") {
		t.Errorf("expected ORDER BY title, got: %s", q)
	}
}

func TestBuildSmartSQL_LimitDefaultsTo100(t *testing.T) {
	q, _ := buildSmartSQL(SmartFilter{}, 0, 0)
	if !strings.Contains(q, "LIMIT 100") {
		t.Errorf("expected LIMIT 100, got: %s", q)
	}
}

func TestBuildSmartSQL_LimitClampedAtUpperBound(t *testing.T) {
	q, _ := buildSmartSQL(SmartFilter{}, 0, 9999)
	if !strings.Contains(q, "LIMIT 100") {
		t.Errorf("expected clamp to 100, got: %s", q)
	}
}
