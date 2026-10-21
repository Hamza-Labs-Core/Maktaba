package supervisor

import (
	"os"
	"path/filepath"
	"strings"
)

// bundleEnv points every role at the ffmpeg of Maktaba.app's payload (its
// ffmpeg/ folder: first on PATH, and the names each service reads), keeping an
// operator's own. Empty outside the app.
func bundleEnv(bundle string) []string {
	if bundle == "" {
		return nil
	}
	dir := filepath.Join(bundle, "ffmpeg")
	if !isExecutable(filepath.Join(dir, "ffmpeg")) {
		return nil
	}
	env := []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}
	for _, b := range []struct{ name, bin string }{
		{"MAKTABA_FFMPEG", "ffmpeg"}, {"MAKTABA_FFMPEG_BIN", "ffmpeg"},
		{"MAKTABA_FFPROBE", "ffprobe"}, {"MAKTABA_FFPROBE_BIN", "ffprobe"},
	} {
		if strings.TrimSpace(os.Getenv(b.name)) == "" {
			env = append(env, b.name+"="+filepath.Join(dir, b.bin))
		}
	}
	return env
}
