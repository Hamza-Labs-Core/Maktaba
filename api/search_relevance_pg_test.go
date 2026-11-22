package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// `maktaba-api search-relevance` on a library with no transcript: the two common words
// find nothing, so the check says so and fails, writing nothing (its session is read only).
func TestSearchRelevanceCLIOnPostgres(t *testing.T) {
	_, dsn := clustertest.ScratchDB(t)
	t.Setenv("DATABASE_URL", dsn)
	var out bytes.Buffer
	err := runSearchRelevance([]string{"--runs", "1"}, &out)
	if err == nil || !strings.Contains(err.Error(), "2 of 2 searches did not pass") {
		t.Fatalf("err %v; out:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "known-item 0 of 0 passed") || !strings.Contains(out.String(), `FAILED common  "الله"`) {
		t.Fatalf("out:\n%s", out.String())
	}
	var help bytes.Buffer
	if err := runSearchRelevance([]string{"--help"}, &help); err != nil || !strings.Contains(help.String(), "-known-item") {
		t.Fatalf("help: %v\n%s", err, help.String())
	}
}
