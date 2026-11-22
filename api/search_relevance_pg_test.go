package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// `maktaba-api search-relevance` on a database whose BM25 indexes are not built: the check
// measures that ranking only, so it refuses (its session is read only: it writes nothing).
func TestSearchRelevanceCLIOnPostgres(t *testing.T) {
	_, dsn := clustertest.ScratchDB(t)
	t.Setenv("DATABASE_URL", dsn)
	var out bytes.Buffer
	err := runSearchRelevance([]string{"--runs", "1"}, &out)
	if err == nil || !strings.Contains(err.Error(), "BM25 indexes are not ready") {
		t.Fatalf("err %v; out:\n%s", err, out.String())
	}
	var help bytes.Buffer
	if err := runSearchRelevance([]string{"--help"}, &help); err != nil || !strings.Contains(help.String(), "-known-item") {
		t.Fatalf("help: %v\n%s", err, help.String())
	}
}
