package flags

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Story 51.19 (run by the catalog job, make catalog-check): every licence a catalog
// component names is a declared flag with a licence feature, and today only cloud.relay
// names one, cloud_relay. No other component is gated (no new paid gate).
func TestCatalogLicence(t *testing.T) {
	gated := map[string]string{}
	for _, c := range components.Default().Components() {
		if c.Licence == "" {
			continue
		}
		if _, err := LicenceFeature(c.Licence); err != nil {
			t.Errorf("%s: %v", c.ID, err)
		}
		gated[c.ID] = c.Licence
	}
	if len(gated) != 1 || gated["cloud.relay"] != FlagCloudRelay {
		t.Fatalf("gated components %v, want only cloud.relay by cloud_relay", gated)
	}
}

// T10: a flag with no licence feature, or no flag at all, is refused.
func TestLicenceFeature(t *testing.T) {
	for _, key := range []string{FlagEarlyAccess, FlagFlagsAdmin, "no_such_flag", ""} {
		if f, err := LicenceFeature(key); err == nil {
			t.Errorf("%q: accepted as %q", key, f)
		}
	}
	for key, want := range map[string]subscriptions.Feature{FlagCloudRelay: subscriptions.FeatureCloudRelay,
		FlagMultiUser: subscriptions.FeatureMultiUser, FlagCloudMetadataBackup: subscriptions.FeatureCloudBackup,
		FlagAdvancedAnalytics: subscriptions.FeatureAdvancedMetric, FlagFederation: subscriptions.FeatureFederation} {
		if f, err := LicenceFeature(key); err != nil || f != want {
			t.Errorf("%q: %q %v", key, f, err)
		}
	}
	// a catalog that names early_access fails the check
	def := components.Default()
	src := `schema = 1
phases = ` + quote(def.Phases) + `
reasons = ` + quote(def.Reasons) + `
features = ` + quote(def.Features) + `
[[component]]
id = "beta"
kind = "process"
group = "system"
title_key = "components.beta.title"
desc_key = "components.beta.desc"
processes = ["fake/beta"]
platforms = ["darwin-arm64"]
licence = "early_access"
since = "0.4.0"
`
	cat, err := components.Load([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := cat.Get("beta")
	if _, err := LicenceFeature(c.Licence); err == nil {
		t.Fatal("early_access passed as a licence")
	}
}

func quote(l []string) string {
	out := "["
	for i, s := range l {
		if i > 0 {
			out += ", "
		}
		out += `"` + s + `"`
	}
	return out + "]"
}
