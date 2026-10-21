package supervisor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// bundleEnv points every role at the ffmpeg of the desktop app's payload (its
// ffmpeg/ folder: first on PATH, and the names each service reads), keeping an
// operator's own. Empty outside the app.
func bundleEnv(bundle string) []string {
	if bundle == "" {
		return nil
	}
	dir := filepath.Join(bundle, "ffmpeg")
	if !isExecutable(filepath.Join(dir, programFile("ffmpeg"))) {
		return nil
	}
	env := []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}
	for _, b := range []struct{ name, bin string }{
		{"MAKTABA_FFMPEG", "ffmpeg"}, {"MAKTABA_FFMPEG_BIN", "ffmpeg"},
		{"MAKTABA_FFPROBE", "ffprobe"}, {"MAKTABA_FFPROBE_BIN", "ffprobe"},
	} {
		if strings.TrimSpace(os.Getenv(b.name)) == "" {
			env = append(env, b.name+"="+filepath.Join(dir, programFile(b.bin)))
		}
	}
	return env
}

// programFile is a program's file name on this OS (ffmpeg.exe on Windows,
// whose payload keeps them so: Epic 38 story 38.11).
func programFile(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
