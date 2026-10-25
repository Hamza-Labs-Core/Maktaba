// Package updaterfeed writes latest.json, the feed the desktop app's updater reads
// (tauri-plugin-updater; Epic 36 story 36.8, Epics 38 and 39), for a release whose
// bundles desktop-release.yml built, checked and staged. It runs once, after every
// platform's leg, over all the staged files: each leg only builds and checks, so a
// bundle is in the feed only when it passed its leg's checks and is attached.
//
// The updater looks a platform up as {os}-{arch}-{installer}, then {os}-{arch}
// (tauri-plugin-updater 2.14, updater.rs), so every signed bundle gets its
// installer's key, and the bundle a platform installs by default also the bare
// one. The bundles are recognised by the names tauri gives them; a signed file
// no rule knows fails the feed rather than leaving a platform out unnoticed.
package updaterfeed

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Platform is one entry of the feed.
type Platform struct {
	Signature string `json:"signature"`
	URL       string `json:"url"`
}

// Feed is latest.json.
type Feed struct {
	Version   string              `json:"version"`
	Notes     string              `json:"notes"`
	PubDate   string              `json:"pub_date"`
	Platforms map[string]Platform `json:"platforms"`
}

// rule names the updater keys of a bundle by its file name: the installer's key
// first, then the bare {os}-{arch} for the platform's default bundle.
type rule struct {
	name *regexp.Regexp
	keys []string
}

// The names tauri gives the updater bundles (desktop-release.yml renames the
// macOS archive, whose name carries no architecture, to Maktaba_<v>_<arch>).
var rules = []rule{
	{regexp.MustCompile(`_aarch64\.app\.tar\.gz$`), []string{"darwin-aarch64-app", "darwin-aarch64"}},
	{regexp.MustCompile(`_x64\.app\.tar\.gz$`), []string{"darwin-x86_64-app", "darwin-x86_64"}},
	{regexp.MustCompile(`_x64-setup\.exe$`), []string{"windows-x86_64-nsis", "windows-x86_64"}},
	{regexp.MustCompile(`_x64_[A-Za-z]{2}-[A-Za-z]{2}\.msi$`), []string{"windows-x86_64-msi"}},
	{regexp.MustCompile(`_amd64\.AppImage$`), []string{"linux-x86_64-appimage", "linux-x86_64"}},
	{regexp.MustCompile(`_aarch64\.AppImage$`), []string{"linux-aarch64-appimage", "linux-aarch64"}},
	{regexp.MustCompile(`_amd64\.deb$`), []string{"linux-x86_64-deb"}},
	{regexp.MustCompile(`_arm64\.deb$`), []string{"linux-aarch64-deb"}},
	{regexp.MustCompile(`\.x86_64\.rpm$`), []string{"linux-x86_64-rpm"}},
	{regexp.MustCompile(`\.aarch64\.rpm$`), []string{"linux-aarch64-rpm"}},
}

// keysFor is the updater keys of a bundle, or nil when no rule knows its name.
func keysFor(name string) []string {
	for _, r := range rules {
		if r.name.MatchString(name) {
			return r.keys
		}
	}
	return nil
}

// Build is the feed of the signed bundles in dir (each <bundle> beside its
// <bundle>.sig), as attached to the release tag of repo ("owner/name").
func Build(dir, repo, tag, notes string, now time.Time) (Feed, error) {
	f := Feed{Version: strings.TrimPrefix(tag, "v"), Notes: notes, PubDate: now.UTC().Format(time.RFC3339),
		Platforms: map[string]Platform{}}
	if f.Version == "" || strings.ContainsAny(tag, "/ ") {
		return f, fmt.Errorf("tag %q: want vX.Y.Z", tag)
	}
	if owner, name, ok := strings.Cut(repo, "/"); !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return f, fmt.Errorf("repository %q: want owner/name", repo)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return f, err
	}
	names := map[string]bool{}
	for _, e := range ents {
		if e.Type().IsRegular() {
			names[e.Name()] = true
		}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, sig := range sorted {
		bundle, ok := strings.CutSuffix(sig, ".sig")
		if !ok {
			continue
		}
		if !names[bundle] {
			return f, fmt.Errorf("%s has no %s beside it", sig, bundle)
		}
		keys := keysFor(bundle)
		if keys == nil {
			return f, fmt.Errorf("a signed bundle no updater key names: %s", bundle)
		}
		b, err := os.ReadFile(filepath.Join(dir, sig))
		if err != nil {
			return f, err
		}
		signature := strings.TrimSpace(string(b))
		if signature == "" {
			return f, fmt.Errorf("%s is empty", sig)
		}
		p := Platform{Signature: signature, URL: "https://github.com/" + repo + "/releases/download/" +
			url.PathEscape(tag) + "/" + url.PathEscape(bundle)}
		for _, k := range keys {
			if prev, dup := f.Platforms[k]; dup {
				return f, fmt.Errorf("two bundles for %s: %s and %s", k, prev.URL, p.URL)
			}
			f.Platforms[k] = p
		}
	}
	return f, nil
}

// errUsage is a usage error (exit 2).
var errUsage = errors.New("usage")

// ExitIncomplete is Main's exit code when the feed was written but lacks a platform
// --require names: the release must not become the latest (desktop-release.yml).
const ExitIncomplete = 3

// Main is the updater-feed command: it writes latest.json for the signed
// bundles in --dir, or nothing (and says so) when there are none, which is a
// release built without the updater key. With --pubkey every bundle's signature is
// checked against the apps' key first (none written when one fails); with --require
// (a comma list of platform keys, or "desktop": Required) a feed that lacks one is
// still written, and the exit code is ExitIncomplete. It returns the exit code.
func Main(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("updater-feed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "the staged release files: the bundles and their .sig files")
	repo := fs.String("repo", "", "the repository the release belongs to (owner/name)")
	tag := fs.String("tag", "", "the release tag (vX.Y.Z)")
	notes := fs.String("notes", "", "the notes the updater shows")
	out := fs.String("out", "latest.json", "where to write the feed")
	pubkey := fs.String("pubkey", "", "the apps' updater public key (TAURI_UPDATER_PUBKEY): every signature is checked against it")
	require := fs.String("require", "", `platform keys the feed must have (comma list, "desktop" or "server-apps")`)
	check := fs.String("check", "", "check this feed (a release's latest.json) has the --require platforms; write nothing")
	verify := fs.Bool("verify", false, "check every signed bundle in --dir against --pubkey for --tag; write nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *check != "" {
		return checkFeed(*check, *require, stdout, stderr)
	}
	if *verify {
		return verifyBundles(*dir, *pubkey, *tag, stdout, stderr)
	}
	if *dir == "" || *repo == "" || *tag == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "updater-feed: %v: --dir DIR --repo OWNER/NAME --tag vX.Y.Z [--notes TEXT] [--out FILE]\n", errUsage)
		return 2
	}
	f, err := Build(*dir, *repo, *tag, *notes, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "updater-feed:", err)
		return 1
	}
	if len(f.Platforms) == 0 {
		_, _ = fmt.Fprintln(stdout, "no signed bundles (no updater key): no latest.json")
		return 0
	}
	if *pubkey != "" {
		if err := VerifyDir(*dir, *pubkey, f.Version); err != nil {
			_, _ = fmt.Fprintln(stderr, "updater-feed: a signature the apps would refuse:", err)
			return 1
		}
	}
	b, _ := json.MarshalIndent(f, "", "  ")
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		_, _ = fmt.Fprintln(stderr, "updater-feed:", err)
		return 1
	}
	keys := make([]string, 0, len(f.Platforms))
	for k := range f.Platforms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	_, _ = fmt.Fprintf(stdout, "%s: %s %s\n", *out, f.Version, strings.Join(keys, " "))
	if miss := Missing(f, Wanted(*require)); len(miss) > 0 {
		_, _ = fmt.Fprintf(stderr, "updater-feed: the feed has no %s: not every platform published\n", strings.Join(miss, ", "))
		return ExitIncomplete
	}
	return 0
}

// verifyBundles checks the release's downloaded bundles as the apps would (VerifyDir): the
// package repositories (packages.yml) publish a Linux app's .deb or .rpm only once its
// signature checks. 0, 1 (one does not check, or none is signed), 2 (usage).
func verifyBundles(dir, pubkey, tag string, stdout, stderr io.Writer) int {
	if dir == "" || pubkey == "" || tag == "" {
		_, _ = fmt.Fprintf(stderr, "updater-feed: %v: --verify --dir DIR --pubkey KEY --tag vX.Y.Z\n", errUsage)
		return 2
	}
	if err := VerifyDir(dir, pubkey, tag); err != nil {
		_, _ = fmt.Fprintln(stderr, "updater-feed: a signature the apps would refuse:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s: every signed bundle checks against the apps' key for %s\n", dir, tag)
	return 0
}

// checkFeed reads a written feed and reports whether it has the required platforms: 0,
// ExitIncomplete (one is missing), 1 (it cannot be read).
func checkFeed(path, require string, stdout, stderr io.Writer) int {
	b, err := os.ReadFile(path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "updater-feed:", err)
		return 1
	}
	var f Feed
	if err := json.Unmarshal(b, &f); err != nil {
		_, _ = fmt.Fprintf(stderr, "updater-feed: %s: %v\n", path, err)
		return 1
	}
	if miss := Missing(f, Wanted(require)); len(miss) > 0 {
		_, _ = fmt.Fprintf(stderr, "updater-feed: %s has no %s\n", path, strings.Join(miss, ", "))
		return ExitIncomplete
	}
	_, _ = fmt.Fprintf(stdout, "%s: %s has every platform asked for\n", path, f.Version)
	return 0
}
