package updater

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// signedRecordName is the signed record of the release an app bundle or a runtime
// committed last (its version, checksums digest and signing time), in its updates folder or
// runtime root: what a layout's watermark is to a plain server (releaseverify.WatermarkFile).
const signedRecordName = "signed-release"

func signedRecord(dir string) releaseverify.WatermarkFile {
	return releaseverify.WatermarkFile{Path: filepath.Join(dir, signedRecordName)}
}

// signedOf is a verified release's signed record.
func signedOf(res *releaseverify.Result) *releaseverify.Watermark {
	if res == nil || res.Manifest == nil {
		return nil
	}
	return &releaseverify.Watermark{Version: res.Manifest.Version, ChecksumsSHA256: res.ChecksumsSHA256, SignedAt: res.SignedAt}
}

// keepSigned writes w as dir's signed record (a commit's: it never goes down).
func keepSigned(dir string, w *releaseverify.Watermark) error {
	if dir == "" || w == nil || w.Version == "" {
		return nil
	}
	f := signedRecord(dir)
	if cur, err := f.Load(); err == nil && cur.Version != "" && releaseverify.CompareVersions(w.Version, cur.Version) < 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	if err := layout.WriteFileAtomic(f.Path+".json", append(b, '\n'), 0o644); err != nil {
		return err
	}
	return layout.WriteFileAtomic(f.Path, []byte(w.Version+"\n"), 0o644)
}

// checkNotBack applies the release rules a layout's watermark applies (downgrade, replay,
// and the one "never go back" rule: releaseverify.OlderBuild) to a release staged for an
// app bundle or a runtime, against dir's signed record when it is the running release's
// (a record of another release, the app's own updater having moved it since, says nothing).
func checkNotBack(dir, running string, res *releaseverify.Result) error {
	w, err := signedRecord(dir).Load()
	if err != nil || w.Version == "" || layout.CompareVersions(w.Version, running) != 0 {
		return nil
	}
	if err := releaseverify.CheckMonotonic(w, res); err != nil {
		return verifyFailure(err)
	}
	return nil
}

// CheckNotBack is checkNotBack for a runtime the `update` command installs into (root,
// running its release running): the one "never go back" rule against the release it
// recorded last. Its failure is a *Failure (older_build, verify_downgrade, verify_replay).
func CheckNotBack(root, running string, res *releaseverify.Result) error {
	return checkNotBack(root, running, res)
}

// KeepSigned records res as root's signed record once it is installed and current.
func KeepSigned(root string, res *releaseverify.Result) error { return keepSigned(root, signedOf(res)) }

// stageRetryDelays are the pauses between a stage's quick tries of its download: a
// download that failed, or arrived damaged (its checksum does not match), is fetched again
// before the step fails (tests shorten them).
var stageRetryDelays = []time.Duration{3 * time.Second, 10 * time.Second}

// retryFetch runs fetch, again after each of stageRetryDelays while it fails with a
// download's failure (download_failed, or verify_checksum: a damaged download); any other
// failure, or one that lasts, is the step's.
func retryFetch(ctx context.Context, fetch func() error) error {
	err := fetch()
	for _, d := range stageRetryDelays {
		var f *Failure
		if err == nil || !errors.As(err, &f) || (f.Code != "download_failed" && f.Code != "verify_checksum") {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(d):
		}
		err = fetch()
	}
	return err
}
