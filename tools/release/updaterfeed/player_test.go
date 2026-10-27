package updaterfeed

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// every player bundle desktop-release.yml's player legs stage (Epic 42.4)
var players = map[string]string{
	"Maktaba-Player_0.3.35_x64.msi":         "msi",
	"Maktaba-Player_0.3.35_x64.msi.sig":     "sig-msi",
	"maktaba-player_0.3.35_amd64.deb":       "deb",
	"maktaba-player_0.3.35_amd64.deb.sig":   "sig-deb-x86",
	"maktaba-player-0.3.35.x86_64.rpm":      "rpm",
	"maktaba-player-0.3.35.x86_64.rpm.sig":  "sig-rpm-x86",
	"maktaba-player_0.3.35_arm64.deb":       "deb",
	"maktaba-player_0.3.35_arm64.deb.sig":   "sig-deb-arm",
	"maktaba-player-0.3.35.aarch64.rpm":     "rpm",
	"maktaba-player-0.3.35.aarch64.rpm.sig": "sig-rpm-arm",
}

func TestPlayerFeedHasItsOwnKeys(t *testing.T) {
	f, err := BuildFamily(PlayerFamily, stage(t, players), "Hamza-Labs-Core/Maktaba", "v0.3.35", "", now)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range f.Platforms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{
		"linux-aarch64-player", "linux-aarch64-player-deb", "linux-aarch64-player-rpm",
		"linux-x86_64-player", "linux-x86_64-player-deb", "linux-x86_64-player-rpm",
		"windows-x86_64-player", "windows-x86_64-player-msi",
	}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys %v, want %v", keys, want)
	}
	// the bare key is the default installer: the .msi, the .deb
	if f.Platforms["windows-x86_64-player"].Signature != "sig-msi" || f.Platforms["linux-x86_64-player"].Signature != "sig-deb-x86" {
		t.Errorf("defaults: %+v", f.Platforms)
	}
	if miss := Missing(f, Wanted("player")); len(miss) != 0 {
		t.Errorf("a complete player feed misses %v", miss)
	}
	// no server key: a server app never reads a player's bundle from it
	for _, k := range Required {
		if _, ok := f.Platforms[k]; ok {
			t.Errorf("the player feed has the server key %s", k)
		}
	}
}

func TestFamiliesRefuseEachOthersBundles(t *testing.T) {
	// a player's .deb matches the server's _amd64.deb rule by its suffix: refused, not filed as a server app
	if _, err := Build(stage(t, map[string]string{
		"maktaba-player_0.3.35_amd64.deb": "deb", "maktaba-player_0.3.35_amd64.deb.sig": "s",
	}), "o/r", "v0.3.35", "", now); err == nil || !strings.Contains(err.Error(), "server") {
		t.Errorf("the server feed took a player bundle: %v", err)
	}
	if _, err := BuildFamily(PlayerFamily, stage(t, map[string]string{
		"Maktaba_0.3.35_amd64.deb": "deb", "Maktaba_0.3.35_amd64.deb.sig": "s",
	}), "o/r", "v0.3.35", "", now); err == nil || !strings.Contains(err.Error(), "player") {
		t.Errorf("the player feed took a server bundle: %v", err)
	}
}

func TestMainWritesThePlayerFeed(t *testing.T) {
	dir := stage(t, players)
	out := filepath.Join(t.TempDir(), "latest-player.json")
	var so, se bytes.Buffer
	if code := Main([]string{"--dir", dir, "--repo", "o/r", "--tag", "v0.3.35", "--family", "player", "--out", out, "--require", "player"}, &so, &se, now); code != 0 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	if b, err := os.ReadFile(out); err != nil || !strings.Contains(string(b), "windows-x86_64-player") {
		t.Fatalf("%v %s", err, b)
	}
	// a player leg that failed: written, but incomplete
	delete(players, "Maktaba-Player_0.3.35_x64.msi")
	delete(players, "Maktaba-Player_0.3.35_x64.msi.sig")
	defer func() {
		players["Maktaba-Player_0.3.35_x64.msi"] = "msi"
		players["Maktaba-Player_0.3.35_x64.msi.sig"] = "sig-msi"
	}()
	if code := Main([]string{"--dir", stage(t, players), "--repo", "o/r", "--tag", "v0.3.35", "--family", "player", "--out", out, "--require", "player"}, &so, &se, now); code != ExitIncomplete {
		t.Errorf("exit %d, want %d", code, ExitIncomplete)
	}
	if code := Main([]string{"--dir", dir, "--repo", "o/r", "--tag", "v0.3.35", "--family", "nope"}, &so, &se, now); code != 2 {
		t.Errorf("an unknown family: exit %d", code)
	}
}
