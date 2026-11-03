package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/componentseed"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
)

// The Linux maktaba-full package (Epic 51 story 51.17): it depends on the app's package
// (maktaba) and adds this release's runtime.ml and tool.yt-dlp packs of its architecture:
// the release's offline file unpacked as /usr/share/maktaba/packs/maktaba-offline-<v>-
// <platform>-full/ (tools/release/linux-app/full-package.sh; an offline .tar there is
// taken as well).
// The server imports it at its first start of that version through 51.16's offline import
// (the same verification as a download: the release's signature, the index, every pack),
// so a Linux Full home builds its venv from those wheels and downloads none. Each file is
// imported once (a marker in <state>/components/.system-imported); a file of another
// version (the app updated, maktaba-full not yet) is left alone, and the packs then come
// from the network as an update brings them (51.18). A home whose setup chose a bundle
// without runtime.ml (Core) imports nothing.

// systemPacksEnv overrides the folder (tests, CI); empty off Linux.
const systemPacksEnv = "MAKTABA_SYSTEM_PACKS"

// systemPacksDir is where the maktaba-full package puts its offline file ("" where there
// is none: macOS and Windows carry no such package).
func systemPacksDir() string {
	if d := strings.TrimSpace(os.Getenv(systemPacksEnv)); d != "" {
		return d
	}
	if goruntime.GOOS != "linux" {
		return ""
	}
	return "/usr/share/maktaba/packs"
}

// packImporter is the packs Manager's offline import (tests replace it).
type packImporter interface {
	Import(ctx context.Context, path string) ([]string, error)
}

// systemPacks is one pass over the folder.
type systemPacks struct {
	dir      string
	stateDir string
	version  string // the running server's, without its "v"
	platform string
	m        packImporter
	log      io.Writer
	now      func() time.Time
}

func (s systemPacks) markerDir() string {
	return filepath.Join(s.stateDir, "components", ".system-imported")
}

// wanted is the home's choice holding runtime.ml (no choice: Full, which does).
func (s systemPacks) wanted() bool {
	ch, ok, err := componentseed.ReadChoice(s.stateDir)
	if err != nil || !ok {
		return true
	}
	return ch.Has("runtime.ml")
}

// run imports each offline file of this version and platform not imported before; it
// returns the names imported now.
func (s systemPacks) run(ctx context.Context) []string {
	if s.dir == "" || s.version == "" {
		return nil
	}
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return nil // no maktaba-full package
	}
	prefix := strings.TrimSuffix(packs.OfflineName(s.version, s.platform, "x"), "x.tar")
	var names []string
	for _, e := range ents {
		n := e.Name()
		// the package's folder (the file unpacked), or an offline file as it is
		if strings.HasPrefix(n, prefix) && (e.IsDir() || e.Type().IsRegular() && strings.HasSuffix(n, ".tar")) {
			names = append(names, n)
		} else if strings.HasPrefix(n, "maktaba-offline-") {
			fmt.Fprintf(s.log, "[packs] %s is not for %s %s: left alone\n", filepath.Join(s.dir, n), s.version, s.platform)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil
	}
	if !s.wanted() {
		fmt.Fprintf(s.log, "[packs] this home's bundle has no runtime.ml: the packs in %s are not imported\n", s.dir)
		return nil
	}
	var done []string
	for _, n := range names {
		marker := filepath.Join(s.markerDir(), n)
		if _, err := os.Stat(marker); err == nil {
			continue
		}
		p := filepath.Join(s.dir, n)
		fmt.Fprintf(s.log, "[packs] importing %s (the maktaba-full package)\n", p)
		ids, err := s.m.Import(ctx, p)
		if err != nil {
			fmt.Fprintf(s.log, "[packs] level=warn %s was not imported: %v (installed: %s)\n", p, err, strings.Join(ids, ", "))
			continue
		}
		if err := os.MkdirAll(s.markerDir(), 0o755); err == nil {
			_ = os.WriteFile(marker, []byte(s.now().UTC().Format(time.RFC3339)+" "+strings.Join(ids, ",")+"\n"), 0o644)
		}
		fmt.Fprintf(s.log, "[packs] %s imported: %s\n", p, strings.Join(ids, ", "))
		done = append(done, n)
	}
	return done
}

// importSystemPacks is serve's first-start import of the maktaba-full package's packs.
func importSystemPacks(ctx context.Context, m *packs.Manager, stateDir string, log io.Writer) {
	systemPacks{dir: systemPacksDir(), stateDir: stateDir, version: m.Version, platform: m.Platform,
		m: m, log: log, now: time.Now}.run(ctx)
}
