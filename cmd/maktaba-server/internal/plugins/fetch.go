package plugins

// A joined machine's copies of its home's plugin packages (story 54.42 §2). The home
// installs and approves; a machine whose placement fits fetches each enabled plugin's
// files over its signed link (the route the home serves to its machines,
// /internal/cluster/plugins/files/…), and verifies them, in this order, before anything
// is written:
//
//   - files.json's sha256 must be the digest the home recorded in plugin_installs (the
//     home's own word on the package, read over the machine's verified database link;
//     only the home writes that table);
//   - every name it lists must be a safe pack path (componentpack.SafePluginPath: no
//     absolute path, "..", backslash, colon or control character), each listed once;
//   - the signatures over files.json must verify against the trust root (pluginsig, as
//     the home's install does: the store's keys for a store.sig) and prove the review
//     level the home recorded: an unsigned package only for an unreviewed install;
//   - then each file is fetched, kept only when its size and sha256 are files.json's,
//     and written only inside the staging folder.
//
// A package that fails is never placed, and the machine reports plugin_package_untrusted.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// HomeFiles reaches the home's package files (a homemirror.Client: signed GETs).
type HomeFiles interface {
	Fetch(ctx context.Context, path string) (*http.Response, error)
}

// filesPath is the home's route for one file of an installed package's folder.
func filesPath(id, folder, name string) string {
	return "/internal/cluster/plugins/files/" + url.PathEscape(id) + "/" + url.PathEscape(folder) + "/" + escapePath(name)
}

func escapePath(p string) string {
	u := url.URL{Path: p}
	return u.EscapedPath()
}

const maxFetch = 64 << 20

func get(ctx context.Context, h HomeFiles, path string) ([]byte, bool, error) {
	resp, err := h.Fetch(ctx, path)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("%s: %s", path, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxFetch+1))
	if err != nil {
		return nil, false, err
	}
	if len(b) > maxFetch {
		return nil, false, fmt.Errorf("%s: over %d bytes", path, maxFetch)
	}
	return b, true, nil
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// FetchFromHome places this machine's copy of in (the home's install row) under the
// layout, verified, and points current at it. A refusal is untrusted, or busy.
func (f *Files) FetchFromHome(ctx context.Context, h HomeFiles, in Install) *Refusal {
	folder := filepath.Base(in.Path)
	dest := filepath.Join(f.Layout.Dir(in.ID), folder)
	if info, err := os.Stat(dest); err == nil && info.IsDir() {
		return nil
	}
	untrusted := func(format string, args ...any) *Refusal {
		return refuse(components.ReasonPluginUntrusted, components.ReasonPluginUntrusted, format, args...)
	}
	filesJSON, ok, err := get(ctx, h, filesPath(in.ID, folder, componentpack.PluginFilesName))
	if err != nil || !ok {
		return refuse(ReasonBusy, ReasonBusy, "files.json from the home: %v", err)
	}
	if hexSum(filesJSON) != in.Digest {
		return untrusted("files.json is %s, the home installed %s", hexSum(filesJSON), in.Digest)
	}
	var list []componentpack.PluginFile
	if err := json.Unmarshal(filesJSON, &list); err != nil {
		return untrusted("files.json: %v", err)
	}
	// every name is checked before anything is fetched or written: the pack rule (no
	// absolute path, no "..", no backslash, colon or control character, none of the
	// package's own names), each once
	var manifestFile *componentpack.PluginFile
	seen := map[string]bool{}
	for i, file := range list {
		if !componentpack.SafePluginPath(file.Path) || seen[file.Path] {
			return untrusted("bad path %q", file.Path)
		}
		seen[file.Path] = true
		if file.Path == componentpack.PluginManifestName {
			manifestFile = &list[i]
		}
	}
	if manifestFile == nil {
		return untrusted("files.json lists no %s", componentpack.PluginManifestName)
	}
	pkg := &componentpack.PluginPackage{FilesJSON: filesJSON, FilesSHA256: in.Digest, Files: list}
	for _, name := range []string{componentpack.PluginDeveloperSig, componentpack.PluginStoreSig} {
		b, ok, err := get(ctx, h, filesPath(in.ID, folder, name))
		if err != nil {
			return refuse(ReasonBusy, ReasonBusy, "%s from the home: %v", name, err)
		}
		if ok {
			if name == componentpack.PluginStoreSig {
				pkg.StoreSig = b
			} else {
				pkg.DeveloperSig = b
			}
		}
	}
	fetch := func(file componentpack.PluginFile) ([]byte, *Refusal) {
		b, ok, err := get(ctx, h, filesPath(in.ID, folder, file.Path))
		if err != nil || !ok {
			return nil, refuse(ReasonBusy, ReasonBusy, "%s from the home: %v", file.Path, err)
		}
		if int64(len(b)) != file.Size || hexSum(b) != file.SHA256 {
			return nil, untrusted("%s does not match files.json", file.Path)
		}
		return b, nil
	}
	// the signature over files.json is verified before anything is written (the chain
	// of trust, as the home's install checks it, 54.14 §3); it needs the manifest's
	// capabilities, so the manifest alone is fetched first and held in memory
	var ref *Refusal
	if pkg.Manifest, ref = fetch(*manifestFile); ref != nil {
		return ref
	}
	m, errs := pluginmanifest.Validate(pkg.Manifest, nil)
	if len(errs) > 0 {
		return untrusted("the manifest: %s", errs.Error())
	}
	// always verified, as the home's install is: the level the signatures prove must be
	// the one the home recorded, so a package whose signatures were stripped on the way
	// never passes for the signed one the home installed
	res, ref := f.verify(pkg, m, in.ID, in.Version)
	if ref != nil {
		return untrusted("%s", ref.Detail)
	}
	if !levelHolds(in.ReviewLevel, res.Level) {
		return untrusted("the signatures prove %q, the home installed it as %q", res.Level, in.ReviewLevel)
	}
	if err := os.MkdirAll(f.Layout.Root, 0o755); err != nil {
		return refuse(ReasonBusy, ReasonBusy, "%v", err)
	}
	tmp := filepath.Join(f.Layout.Root, newStaging())
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(tmp)
		}
	}()
	write := func(name string, b []byte) error {
		p, err := contained(tmp, name)
		if err != nil || !strings.HasPrefix(p, filepath.Clean(tmp)+string(os.PathSeparator)) {
			return fmt.Errorf("%q is outside the package folder", name)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, b, 0o644)
	}
	if err := write(componentpack.PluginFilesName, filesJSON); err != nil {
		return refuse(ReasonBusy, ReasonBusy, "%v", err)
	}
	for _, file := range list {
		b := pkg.Manifest
		if file.Path != componentpack.PluginManifestName {
			if b, ref = fetch(file); ref != nil {
				return ref
			}
		}
		if err := write(file.Path, b); err != nil {
			return untrusted("%v", err)
		}
	}
	for name, b := range map[string][]byte{componentpack.PluginDeveloperSig: pkg.DeveloperSig, componentpack.PluginStoreSig: pkg.StoreSig} {
		if b != nil {
			if err := write(name, b); err != nil {
				return refuse(ReasonBusy, ReasonBusy, "%v", err)
			}
		}
	}
	unlock, err := f.Layout.Lock(ctx, in.ID)
	if err != nil {
		return refuse(ReasonBusy, ReasonBusy, "%v", err)
	}
	defer unlock()
	if err := os.MkdirAll(f.Layout.Dir(in.ID), 0o755); err != nil {
		return refuse(ReasonBusy, ReasonBusy, "%v", err)
	}
	if err := os.Rename(tmp, dest); err != nil && !errors.Is(err, os.ErrExist) {
		return refuse(ReasonBusy, ReasonBusy, "rename %s: %v", dest, err)
	}
	keep = true
	if ref := f.preload(ctx, in.ID, in.Version, dest); ref != nil {
		_ = os.RemoveAll(dest)
		return ref
	}
	if err := f.Layout.SetCurrent(in.ID, folder); err != nil {
		return refuse(ReasonBusy, ReasonBusy, "%v", err)
	}
	return nil
}

// contained is name under dir, refused when it would land outside it (a name that passed
// SafePluginPath never does; the check holds at the write itself).
func contained(dir, name string) (string, error) {
	p := filepath.Clean(filepath.Join(dir, filepath.FromSlash(name)))
	if !strings.HasPrefix(p, filepath.Clean(dir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("%q is outside the package folder", name)
	}
	return p, nil
}

// levelHolds reports whether a package whose signatures prove level proved is the one the
// home installed at level recorded. An unreviewed install (none, or a development build's
// development) needs no signature: the gate holds it behind the admin's opt-in to
// unreviewed plugins (54.10 §3), on the home and on every machine. Any other level must
// be proven exactly; the earlier "store" level is either store-signed one.
func levelHolds(recorded, proved string) bool {
	switch recorded {
	case pluginsig.LevelNone, "development":
		return true
	case "store":
		return proved == pluginsig.LevelAutomated || proved == pluginsig.LevelReviewed
	case "":
		return false
	}
	return proved == recorded
}
