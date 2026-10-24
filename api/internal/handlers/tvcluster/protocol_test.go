package tvcluster

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// ProtocolVersion mirrors streaming's (streaming/internal/cluster/host.go). A mismatch
// showed every host of the current release as "Not fit: version" on the Live TV page.
func TestProtocolVersionMirrorsStreamings(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "streaming", "internal", "cluster", "host.go"))
	if err != nil {
		t.Skipf("streaming's source is not in this checkout: %v", err)
	}
	m := regexp.MustCompile(`(?m)^const ProtocolVersion = (\d+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no ProtocolVersion in streaming/internal/cluster/host.go")
	}
	if want, _ := strconv.Atoi(string(m[1])); ProtocolVersion != want {
		t.Fatalf("ProtocolVersion = %d, streaming's = %d", ProtocolVersion, want)
	}
}
