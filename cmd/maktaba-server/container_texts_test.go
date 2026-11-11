package main

import (
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// Story 59.14, T03: a message a container prints before the web is up (the preflight's
// refusals, the setup hint, the bridge and moved-port notes of 59.08) is read in a NAS's log
// viewer, which cannot switch language: it is two lines, English, then Arabic.

// codeOrName drops what stays Latin in Arabic text: `code`, {placeholders}, paths, and the
// product's and the NAS's names.
var codeOrName = regexp.MustCompile("`[^`]*`|\\{[a-z_]+\\}|\\S*/\\S*|Maktaba|TrueNAS|Unraid|DSM|Container Manager|Docker|HTTPS|ACL|Apps|Installed|Workloads|Shell|Console|File Station|maktaba|Cloud")

func letters(s string) (latin, arabic int) {
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Arabic, r):
			arabic++
		case r < 128 && unicode.IsLetter(r):
			latin++
		}
	}
	return
}

func checkTwoLines(t *testing.T, name, msg string) {
	t.Helper()
	lines := strings.Split(msg, "\n")
	if len(lines) != 2 {
		t.Fatalf("%s: %d lines, want 2 (English, then Arabic): %q", name, len(lines), msg)
	}
	if _, ar := letters(lines[0]); ar != 0 || lines[0] == "" {
		t.Errorf("%s: the first line is not English: %q", name, lines[0])
	}
	latin, ar := letters(codeOrName.ReplaceAllString(lines[1], " "))
	if ar == 0 || latin*5 > ar {
		t.Errorf("%s: the second line is not Arabic (%d Latin, %d Arabic letters): %q", name, latin, ar, lines[1])
	}
}

func TestContainerTextsAreEnglishThenArabic(t *testing.T) {
	for key := range containerTexts {
		checkTwoLines(t, key, containerText(key, "{path}", "/var/lib/maktaba", "{uid}", "1026", "{gid}", "100"))
	}
}

func TestSetupHintPerPlatform(t *testing.T) {
	for platform, want := range map[string]string{
		"truenas": "Shell",
		"dsm":     "Container Manager",
		"unraid":  "Console",
		"":        "docker compose run --rm maktaba setup",
		"docker":  "docker compose run --rm maktaba setup",
	} {
		h := setupHint(platform)
		checkTwoLines(t, "setup hint "+platform, h)
		if !strings.Contains(h, want) {
			t.Errorf("%q: the hint does not name %q: %q", platform, want, h)
		}
	}
}

// The 59.08 notes the container log prints come from setup's own texts, in both languages.
func TestContainerNetworkNotesAreBilingual(t *testing.T) {
	for _, key := range []string{
		"setup.check.note.network_bridge",
		"setup.check.note.port_moved",
		"setup.check.note.port_moved_other",
	} {
		pair, ok := webSetupTexts[key]
		if !ok {
			t.Fatalf("%s: not in setup's texts", key)
		}
		checkTwoLines(t, key, pair[0]+"\n"+pair[1])
	}
	en, ar := bridgeText("http://192.168.1.20:8088")
	checkTwoLines(t, "bridge", en+"\n"+ar)
}
