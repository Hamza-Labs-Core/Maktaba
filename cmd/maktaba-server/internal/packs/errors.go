// Package packs installs, removes and imports the asset components of Epic 51 (story
// 51.16): a pack (a .tar.zst of one asset's files, built by the release) or a pinned
// model, checked against the release's signed component index before anything is used.
//
// Everything lives under one folder, <state>/components (config.StateDir: the spec's
// <data>):
//
//	<root>/
//	  runtime.ml/
//	    0.4.2_3f9a1c2b7d10/   the unpacked pack's files (wheels/, constraints.txt, ...)
//	    current               "0.4.2_3f9a1c2b7d10\n" (a pointer file, never a symlink)
//	    .lock                 one install or removal at a time, across processes
//	    .removing             a removal that has not finished (the next boot finishes it)
//	  model.whisper-turbo/
//	    rev_<revision12>/model.json   the files live in the Hugging Face cache
//	    current
//	  .partial/               downloads in progress (resumed with HTTP Range)
//
// Nothing in a pack is run: the unpacker only writes files (componentpack.Unpack), and
// what each asset needs after that (the pipeline's venv for runtime.ml) is a Prepare
// hook the server gives.
package packs

import (
	"errors"
	"fmt"
)

// Reason codes an install, a removal or an import fails with (the catalog's reasons:
// each has English and Arabic words, components.reason.<code>).
const (
	ReasonIndexMissing      = "index_missing"
	ReasonIndexExpired      = "index_expired"
	ReasonIndexWrongVersion = "index_wrong_version"
	ReasonIndexReplayed     = "index_replayed"
	ReasonVerifyFailed      = "verify_failed"
	// ReasonOfflineVerifyFailed is verify_failed of an offline file's copy (an import): the
	// file did not match the signed index it carries; name is the item's file.
	ReasonOfflineVerifyFailed = "offline_verify_failed"
	ReasonDownloadFailed      = "download_failed"
	ReasonDiskSpace           = "disk_space"
	ReasonUnpackRefused       = "unpack_refused"
	ReasonOfflineWrongVersion = "offline_wrong_version"
	ReasonOfflineWrongPlat    = "offline_wrong_platform"
	ReasonPacksDisabled       = "packs_disabled"
	ReasonPackMissing         = "pack_missing"
	// ReasonPackNotPublished: the release has none of its signed files yet (a version not
	// published, or not with its packs): the install fails until it is; name is the asset.
	ReasonPackNotPublished = "pack_not_published"
	ReasonInstallFailed    = "install_failed"
	ReasonBusy             = "busy"
	ReasonPlatform         = "platform_unsupported"
	ReasonRequiredBy       = "required_by"
)

// Reasons is every reason code this package reports (the catalog must list each).
var Reasons = []string{ReasonIndexMissing, ReasonIndexExpired, ReasonIndexWrongVersion, ReasonIndexReplayed,
	ReasonVerifyFailed, ReasonOfflineVerifyFailed, ReasonDownloadFailed, ReasonDiskSpace, ReasonUnpackRefused, ReasonOfflineWrongVersion,
	ReasonOfflineWrongPlat, ReasonPacksDisabled, ReasonPackMissing, ReasonPackNotPublished, ReasonInstallFailed, ReasonBusy,
	ReasonPlatform, ReasonRequiredBy}

// Detail codes of an install's steps (components.detail.<code> in the web's words).
const (
	DetailIndex       = "pack_index"
	DetailDownloading = "pack_downloading"
	DetailVerifying   = "pack_verifying"
	DetailUnpacking   = "pack_unpacking"
	DetailPreparing   = "pack_preparing"
	DetailRemoving    = "pack_removing"
	DetailImporting   = "pack_importing"
	DetailOffline     = "pack_offline"
)

// Details is every detail code this package reports.
var Details = []string{DetailIndex, DetailDownloading, DetailVerifying, DetailUnpacking, DetailPreparing,
	DetailRemoving, DetailImporting, DetailOffline}

// Error is a failed install, removal or import: its reason code, the reason's words'
// values, and the cause.
type Error struct {
	Reason string
	Params map[string]any
	Err    error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Reason + ": " + e.Err.Error()
	}
	return e.Reason
}

func (e *Error) Unwrap() error { return e.Err }

func fail(reason string, params map[string]any, format string, a ...any) *Error {
	var err error
	if format != "" {
		err = fmt.Errorf(format, a...)
	}
	return &Error{Reason: reason, Params: params, Err: err}
}

func wrap(reason string, params map[string]any, err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Reason: reason, Params: params, Err: err}
}

// fromFile is err said of an offline file: verify_failed becomes offline_verify_failed
// (the words name the file, not a download); any other error is err.
func fromFile(err error) error {
	var e *Error
	if errors.As(err, &e) && e.Reason == ReasonVerifyFailed {
		return &Error{Reason: ReasonOfflineVerifyFailed, Params: e.Params, Err: e.Err}
	}
	return err
}

// ReasonOf is err's reason code ("" for nil, install_failed for an error of no reason).
func ReasonOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return ReasonInstallFailed
}

// ParamsOf is err's reason's values (nil when none).
func ParamsOf(err error) map[string]any {
	var e *Error
	if errors.As(err, &e) {
		return e.Params
	}
	return nil
}
