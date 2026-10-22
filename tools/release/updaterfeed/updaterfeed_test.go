package updaterfeed

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 21, 9, 30, 0, 0, time.FixedZone("x", 3*3600))

// stage writes files (name -> content) into a new directory.
func stage(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// every bundle desktop-release.yml stages, as tauri names them
var release = map[string]string{
	"Maktaba_0.3.0_aarch64.dmg":            "dmg",
	"Maktaba_0.3.0_aarch64.app.tar.gz":     "app",
	"Maktaba_0.3.0_aarch64.app.tar.gz.sig": "sig-mac-arm\n",
	"Maktaba_0.3.0_x64.app.tar.gz":         "app",
	"Maktaba_0.3.0_x64.app.tar.gz.sig":     "sig-mac-intel",
	"Maktaba_0.3.0_x64-setup.exe":          "exe",
	"Maktaba_0.3.0_x64-setup.exe.sig":      "sig-nsis",
	"Maktaba_0.3.0_x64_en-US.msi":          "msi",
	"Maktaba_0.3.0_x64_en-US.msi.sig":      "sig-msi",
	"maktaba-windows-portable-0.3.0.zip":   "zip",
	"Maktaba_0.3.0_amd64.AppImage":         "ai",
	"Maktaba_0.3.0_amd64.AppImage.sig":     "sig-ai-x86",
	"Maktaba_0.3.0_amd64.deb":              "deb",
	"Maktaba_0.3.0_amd64.deb.sig":          "sig-deb-x86",
	"Maktaba-0.3.0-1.x86_64.rpm":           "rpm",
	"Maktaba-0.3.0-1.x86_64.rpm.sig":       "sig-rpm-x86",
	"Maktaba_0.3.0_aarch64.AppImage":       "ai",
	"Maktaba_0.3.0_aarch64.AppImage.sig":   "sig-ai-arm",
	"Maktaba_0.3.0_arm64.deb":              "deb",
	"Maktaba_0.3.0_arm64.deb.sig":          "sig-deb-arm",
	"Maktaba-0.3.0-1.aarch64.rpm":          "rpm",
	"Maktaba-0.3.0-1.aarch64.rpm.sig":      "sig-rpm-arm",
}

func TestBuildNamesEveryInstallerAndTheDefaults(t *testing.T) {
	f, err := Build(stage(t, release), "Hamza-Labs-Core/Maktaba", "v0.3.0", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != "0.3.0" || f.PubDate != "2026-10-21T06:30:00Z" {
		t.Errorf("version %q pub_date %q", f.Version, f.PubDate)
	}
	base := "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v0.3.0/"
	want := map[string][2]string{
		"darwin-aarch64-app":     {"sig-mac-arm", "Maktaba_0.3.0_aarch64.app.tar.gz"},
		"darwin-aarch64":         {"sig-mac-arm", "Maktaba_0.3.0_aarch64.app.tar.gz"},
		"darwin-x86_64-app":      {"sig-mac-intel", "Maktaba_0.3.0_x64.app.tar.gz"},
		"darwin-x86_64":          {"sig-mac-intel", "Maktaba_0.3.0_x64.app.tar.gz"},
		"windows-x86_64-nsis":    {"sig-nsis", "Maktaba_0.3.0_x64-setup.exe"},
		"windows-x86_64":         {"sig-nsis", "Maktaba_0.3.0_x64-setup.exe"},
		"windows-x86_64-msi":     {"sig-msi", "Maktaba_0.3.0_x64_en-US.msi"},
		"linux-x86_64-appimage":  {"sig-ai-x86", "Maktaba_0.3.0_amd64.AppImage"},
		"linux-x86_64":           {"sig-ai-x86", "Maktaba_0.3.0_amd64.AppImage"},
		"linux-x86_64-deb":       {"sig-deb-x86", "Maktaba_0.3.0_amd64.deb"},
		"linux-x86_64-rpm":       {"sig-rpm-x86", "Maktaba-0.3.0-1.x86_64.rpm"},
		"linux-aarch64-appimage": {"sig-ai-arm", "Maktaba_0.3.0_aarch64.AppImage"},
		"linux-aarch64":          {"sig-ai-arm", "Maktaba_0.3.0_aarch64.AppImage"},
		"linux-aarch64-deb":      {"sig-deb-arm", "Maktaba_0.3.0_arm64.deb"},
		"linux-aarch64-rpm":      {"sig-rpm-arm", "Maktaba-0.3.0-1.aarch64.rpm"},
	}
	if len(f.Platforms) != len(want) {
		t.Errorf("%d platforms, want %d: %v", len(f.Platforms), len(want), f.Platforms)
	}
	for k, w := range want {
		p, ok := f.Platforms[k]
		if !ok || p.Signature != w[0] || p.URL != base+w[1] {
			t.Errorf("%s = %+v, want %s %s", k, p, w[0], base+w[1])
		}
	}
}

// A leg that failed staged nothing: its platforms are absent, the others are there.
func TestBuildWithAMissingLeg(t *testing.T) {
	files := map[string]string{}
	for n, b := range release {
		if !strings.Contains(n, "aarch64") && !strings.Contains(n, "arm64") {
			files[n] = b
		}
	}
	f, err := Build(stage(t, files), "o/r", "v0.3.0-rc.1", "notes", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Platforms["linux-aarch64"]; ok {
		t.Error("a platform whose leg failed is in the feed")
	}
	if _, ok := f.Platforms["linux-x86_64-rpm"]; !ok || f.Version != "0.3.0-rc.1" || f.Notes != "notes" {
		t.Errorf("feed %+v", f)
	}
}

func TestBuildRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		files     map[string]string
		repo, tag string
		want      string
	}{
		"an unknown signed file": {map[string]string{"Maktaba.tar.gz": "x", "Maktaba.tar.gz.sig": "s"}, "o/r", "v1.0.0", "no updater key"},
		"a signature alone":      {map[string]string{"Maktaba_1.0.0_amd64.deb.sig": "s"}, "o/r", "v1.0.0", "has no"},
		"an empty signature":     {map[string]string{"Maktaba_1.0.0_amd64.deb": "d", "Maktaba_1.0.0_amd64.deb.sig": " \n"}, "o/r", "v1.0.0", "is empty"},
		"two bundles for a key": {map[string]string{"A_1_amd64.deb": "d", "A_1_amd64.deb.sig": "s", "B_1_amd64.deb": "d",
			"B_1_amd64.deb.sig": "s"}, "o/r", "v1.0.0", "two bundles"},
		"no tag":      {nil, "o/r", "", "tag"},
		"a bad repo":  {nil, "nope", "v1.0.0", "repository"},
		"a deep repo": {nil, "a/b/c", "v1.0.0", "repository"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Build(stage(t, tc.files), tc.repo, tc.tag, "", now)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := Build(filepath.Join(t.TempDir(), "missing"), "o/r", "v1.0.0", "", now); err == nil {
		t.Error("a missing directory")
	}
}

func TestMain(t *testing.T) {
	dir := stage(t, release)
	out := filepath.Join(t.TempDir(), "latest.json")
	var so, se bytes.Buffer
	if code := Main([]string{"--dir", dir, "--repo", "o/r", "--tag", "v0.3.0", "--out", out}, &so, &se, now); code != 0 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	var f Feed
	b, _ := os.ReadFile(out)
	if err := json.Unmarshal(b, &f); err != nil || len(f.Platforms) != 15 || !strings.Contains(so.String(), "linux-aarch64-rpm") {
		t.Errorf("feed %s (%v), stdout %q", b, err, so.String())
	}

	// no signatures (no updater key): nothing written, not a failure
	empty := stage(t, map[string]string{"Maktaba_0.3.0_amd64.deb": "d"})
	out2 := filepath.Join(t.TempDir(), "latest.json")
	so.Reset()
	if code := Main([]string{"--dir", empty, "--repo", "o/r", "--tag", "v0.3.0", "--out", out2}, &so, &se, now); code != 0 ||
		!strings.Contains(so.String(), "no latest.json") {
		t.Errorf("no signatures: exit %d, %q", code, so.String())
	}
	if _, err := os.Stat(out2); !os.IsNotExist(err) {
		t.Error("a feed was written without signatures")
	}

	se.Reset()
	if code := Main([]string{"--dir", dir}, &so, &se, now); code != 2 || !strings.Contains(se.String(), "usage") {
		t.Errorf("missing flags: exit %d, %q", code, se.String())
	}
	if code := Main([]string{"--bogus"}, &so, &se, now); code != 2 {
		t.Errorf("unknown flag: exit %d", code)
	}
	bad := stage(t, map[string]string{"x.sig": "s"})
	if code := Main([]string{"--dir", bad, "--repo", "o/r", "--tag", "v1.0.0"}, &so, &se, now); code != 1 {
		t.Errorf("a bad staging directory: exit %d", code)
	}
	if code := Main([]string{"--dir", dir, "--repo", "o/r", "--tag", "v1.0.0", "--out", filepath.Join(dir, "no", "such", "f")}, &so, &se, now); code != 1 {
		t.Errorf("an unwritable --out: exit %d", code)
	}
}
