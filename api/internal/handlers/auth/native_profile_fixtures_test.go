package auth

// The Android client's sessions fixture (story 48.31, ProfileApiTest) decoded into the answer
// MeSessions writes, refusing any field it does not have, so the fixture cannot drift from the
// API. The other Profile fixtures are checked in handlers/browse.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAndroidSessionsFixtureMatchesTheHandler(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "apps", "android", "client", "src", "test", "resources", "profile", "sessions.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Items []sessionView `json:"items"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("sessions.json does not decode as MeSessions' answer: %v", err)
	}
	if len(doc.Items) == 0 {
		t.Fatal("sessions.json has no sessions")
	}
}
