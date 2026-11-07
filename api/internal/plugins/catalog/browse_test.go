package catalog

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// Story 54.18 T01: on 0.6.2 only versions whose range includes 0.6.2 show, each plugin with
// its newest compatible version; a newer plugin API or another platform is left out.
func TestNewestCompatible(t *testing.T) {
	v := func(ver, rng, api string, plat ...string) pluginsig.CatalogVersion {
		if plat == nil {
			plat = []string{"any"}
		}
		return pluginsig.CatalogVersion{Version: ver, Maktaba: rng, PluginAPI: api, Platforms: plat}
	}
	vs := []pluginsig.CatalogVersion{
		v("1.0.0", ">=0.6.0, <0.7.0", "1.0"),
		v("1.1.0", ">=0.6.0, <0.7.0", "1.0"),
		v("2.0.0", ">=0.7.0", "1.0"),
		v("1.2.0", ">=0.6.0", "1.9"),
		v("1.3.0", ">=0.6.0", "1.0", "linux-amd64"),
	}
	got := newest(vs, "0.6.2", "darwin-arm64")
	if got == nil || got.Version != "1.1.0" {
		t.Fatalf("newest %+v", got)
	}
	if newest(vs[2:3], "0.6.2", "darwin-arm64") != nil {
		t.Fatal("a version needing 0.7.0 shows on 0.6.2")
	}
	if g := newest(vs, "0.6.2", "linux-amd64"); g == nil || g.Version != "1.3.0" {
		t.Fatalf("the platform's own: %+v", g)
	}
}
