package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
)

// Story 60.07 T01 (the Go half): what the site's build reads is the policy the relay serves.
func TestPrintsWhatTheRelayServes(t *testing.T) {
	var out bytes.Buffer
	if err := write(&out); err != nil {
		t.Fatal(err)
	}
	var printed privacy.Policy
	if err := json.Unmarshal(out.Bytes(), &printed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if !reflect.DeepEqual(printed, privacy.CurrentPolicy()) {
		t.Fatalf("printed %+v, want CurrentPolicy() %+v", printed, privacy.CurrentPolicy())
	}

	rec := httptest.NewRecorder()
	privacy.PolicyHandler(rec, httptest.NewRequest("GET", "/privacy", nil))
	var served privacy.Policy
	if err := json.Unmarshal(rec.Body.Bytes(), &served); err != nil {
		t.Fatalf("GET /privacy is not JSON: %v", err)
	}
	if !reflect.DeepEqual(printed, served) {
		t.Fatalf("printed %+v, GET /privacy served %+v", printed, served)
	}
}

// The build relies on every field being present (no omitempty) and on the keys it reads.
func TestPrintsEveryFieldTheBuildReads(t *testing.T) {
	var out bytes.Buffer
	if err := write(&out); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"controller", "contact", "purpose", "data_categories",
		"not_collected", "lawful_basis", "retention_days", "rights", "updated_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("no %q in %s", k, out.String())
		}
	}
	if !bytes.HasSuffix(out.Bytes(), []byte("}\n")) {
		t.Errorf("want one trailing newline, got %q", out.String()[out.Len()-3:])
	}
}
