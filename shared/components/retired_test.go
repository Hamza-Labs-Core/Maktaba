package components

import (
	"strings"
	"testing"
)

// The retired names are gone from the catalog (the rows the lossless slot 0215 kept are
// all that is left of them), and the helpers and SQL conditions answer for exactly them.
func TestRetiredNames(t *testing.T) {
	cat := Default()
	for _, id := range RetiredComponents {
		if _, ok := cat.Get(id); ok {
			t.Errorf("retired component %s is still in the catalog", id)
		}
	}
	for _, s := range RetiredStages {
		if c, ok := cat.ByStage(s); ok && c != nil {
			t.Errorf("retired stage %s still belongs to %s", s, c.ID)
		}
		if !RetiredStage(s) {
			t.Errorf("RetiredStage(%s) false", s)
		}
	}
	if RetiredStage("plex_livetv") || RetiredPool("network_plex") || RetiredExtension("plex") || RetiredComponent("ext.plex") {
		t.Error("Plex Live TV is not retired")
	}
	if !RetiredPool("network_tv") || !RetiredExtension("dizquetv") || !RetiredComponent("ext.dizquetv") {
		t.Error("the dizqueTV names are retired")
	}
	got := NotRetiredStageSQL("j.stage")
	if !strings.HasPrefix(got, "j.stage NOT IN ('dizquetv_audit', ") || !strings.Contains(got, "'plex_groups'") {
		t.Errorf("stage condition %q", got)
	}
	if got := NotRetiredPoolSQL("pool"); got != "pool NOT IN ('audits', 'network_tv')" {
		t.Errorf("pool condition %q", got)
	}
}
