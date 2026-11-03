package updaterfeed

import "testing"

// T08: a release with both Mac apps: the Core keys name the Core app, the Full keys are
// unchanged.
func TestBuildCoreKeys(t *testing.T) {
	files := map[string]string{}
	for n, b := range release {
		files[n] = b
	}
	files["Maktaba-Core_0.3.0_aarch64.app.tar.gz"] = "core"
	files["Maktaba-Core_0.3.0_aarch64.app.tar.gz.sig"] = "sig-mac-core"
	f, err := Build(stage(t, files), "o/r", "v0.3.0", "", now)
	if err != nil {
		t.Fatal(err)
	}
	base := "https://github.com/o/r/releases/download/v0.3.0/"
	for k, w := range map[string][2]string{
		"darwin-aarch64-core-app": {"sig-mac-core", "Maktaba-Core_0.3.0_aarch64.app.tar.gz"},
		"darwin-aarch64-core":     {"sig-mac-core", "Maktaba-Core_0.3.0_aarch64.app.tar.gz"},
		"darwin-aarch64-app":      {"sig-mac-arm", "Maktaba_0.3.0_aarch64.app.tar.gz"},
		"darwin-aarch64":          {"sig-mac-arm", "Maktaba_0.3.0_aarch64.app.tar.gz"},
	} {
		if p := f.Platforms[k]; p.Signature != w[0] || p.URL != base+w[1] {
			t.Errorf("%s = %+v, want %s %s", k, p, w[0], w[1])
		}
	}
}
