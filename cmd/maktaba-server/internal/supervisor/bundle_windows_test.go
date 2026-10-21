//go:build windows

package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 38 story 38.10: the Windows payload keeps ffmpeg.exe and ffprobe.exe
// (story 38.11); every role gets them.
func TestAppBundleFFmpegOnWindows(t *testing.T) {
	for _, k := range []string{"MAKTABA_FFMPEG", "MAKTABA_FFMPEG_BIN", "MAKTABA_FFPROBE", "MAKTABA_FFPROBE_BIN"} {
		t.Setenv(k, "")
	}
	p := t.TempDir()
	ff := filepath.Join(p, "ffmpeg")
	if err := os.MkdirAll(ff, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := bundleEnv(p); got != nil {
		t.Fatalf("no ffmpeg.exe yet: %v", got)
	}
	for _, f := range []string{"ffmpeg.exe", "ffprobe.exe"} {
		if err := os.WriteFile(filepath.Join(ff, f), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MAKTABA_APP_BUNDLE", p)
	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		m := envMap(ChildEnv(role, config.Defaults()))
		if m["MAKTABA_FFMPEG"] != filepath.Join(ff, "ffmpeg.exe") || m["MAKTABA_FFPROBE_BIN"] != filepath.Join(ff, "ffprobe.exe") ||
			!strings.HasPrefix(m["PATH"], ff+string(os.PathListSeparator)) {
			t.Errorf("%s: ffmpeg env = %v", role, m)
		}
	}
}
