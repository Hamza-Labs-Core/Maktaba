package sharepath

import "testing"

// The API's copy is the shared rules (their fixtures are tested in shared/go/mediaaccess).
func TestNASRulesAreTheSharedOnes(t *testing.T) {
	if s, root, ok := RuleShare("dsm", "/volume1/Movies/en"); !ok || s != "Movies" || root != "/volume1/Movies" {
		t.Fatalf("dsm: %q %q %v", s, root, ok)
	}
	if _, _, ok := RuleShare("truenas", "/mnt/tank/media"); ok {
		t.Fatal("truenas: share names are not in the path")
	}
	if IsHostPath("dsm", "/media/Movies") || !IsHostPath("truenas", "/mnt/tank/media") {
		t.Fatal("host paths")
	}
}
