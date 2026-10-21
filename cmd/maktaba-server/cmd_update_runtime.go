package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/selfupdate"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// `update` in the Linux app's runtime copy (Epic 39 stories 39.3, 39.9,
// contract-runtime.md §4). The runtime's owner says who updates it: the app
// (its updater brings a new payload, the shell activates it), or with owner
// headless the machine itself, from the same signed releases as every other
// install: the release's payload tarball is verified against checksums.txt,
// unpacked, installed as a new runtime directory and activated (the service
// stopped while `current` moves). The running binary is never replaced.

// releaseSource is what a runtime update reads from GitHub Releases (tests
// replace it).
type releaseSource interface {
	Latest(ctx context.Context) (*selfupdate.Release, error)
	ByTag(ctx context.Context, version string) (*selfupdate.Release, error)
	FetchAsset(ctx context.Context, rel *selfupdate.Release, name, dir string) (string, *releaseverify.Result, error)
}

// errAppOwnsRuntime: an `update` of a runtime the app updates.
var errAppOwnsRuntime = errors.New("this server is the Linux app's: the app updates it (Check for Updates in its menu); " +
	"`maktaba-server runtime install --owner headless` hands its updates to this machine")

func runRuntimeUpdate(ctx context.Context, out io.Writer, d runtimeDeps, src releaseSource, target string, check, rollback bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if d.rt.ReadOwner() != appruntime.OwnerHeadless {
		return errAppOwnsRuntime
	}
	if rollback {
		prev, err := d.rt.Activate(ctx, appruntime.PreviousLink, d.service, appruntime.Options{AllowDowngrade: true})
		if err != nil {
			return err
		}
		cur, _ := d.rt.Current()
		fmt.Fprintf(out, "current = %s (was %s): the runtime that ran before.\n", cur, orNone(prev))
		return nil
	}
	st, err := d.rt.Status("")
	if err != nil {
		return err
	}
	var rel *selfupdate.Release
	if target != "" {
		rel, err = src.ByTag(ctx, target)
	} else {
		rel, err = src.Latest(ctx)
	}
	if errors.Is(err, selfupdate.ErrNoRelease) {
		fmt.Fprintln(out, "No matching release found.")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "current: %s\n", orNone(st.Version))
	fmt.Fprintf(out, "release: %s\n", rel.Tag)
	if target == "" && appruntime.CompareVersions(rel.Tag, st.Version) <= 0 {
		fmt.Fprintln(out, "You are up to date.")
		return nil
	}
	// beside the runtime, on its disk; Install and prune remove one an update left
	dl, err := os.MkdirTemp(d.rt.Root, appruntime.DownloadPrefix)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dl) }()
	asset := appruntime.ReleaseAsset(rel.Tag)
	fmt.Fprintf(out, "Downloading and verifying %s ...\n", asset)
	tarball, res, err := src.FetchAsset(ctx, rel, asset, dl)
	if err != nil {
		return fmt.Errorf("refusing %s: %w", rel.Tag, err)
	}
	fmt.Fprintf(out, "Verified %s: signed by %s at %s.\n", rel.Tag, res.Identity, res.SignedAt.Format("2006-01-02 15:04:05Z"))
	if check {
		fmt.Fprintln(out, "(--check) not installing. Re-run without --check to upgrade.")
		return nil
	}
	payload, err := appruntime.Unpack(tarball, filepath.Join(dl, "payload"))
	if err != nil {
		return err
	}
	_ = os.Remove(tarball) // its room is the copy's
	p, err := appruntime.ReadPayload(payload)
	if err != nil {
		return err
	}
	if layout.CompareVersions(p.Version, res.Manifest.Version) != 0 {
		return fmt.Errorf("%w: %s holds the payload of %s, not of %s", appruntime.ErrInvalid, asset, p.Version, res.Manifest.Version)
	}
	r, err := d.rt.Install(ctx, payload, nil, appruntime.Options{})
	if err != nil {
		return err
	}
	if r.First {
		fmt.Fprintf(out, "current = %s.\n", filepath.Base(r.Dir))
		return nil
	}
	prev, err := d.rt.Activate(ctx, filepath.Base(r.Dir), d.service, appruntime.Options{})
	if err != nil {
		return err
	}
	pruned, err := d.rt.Prune(ctx, 2)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "current = %s (was %s); the server restarted on it. `maktaba-server update --rollback` goes back.\n",
		filepath.Base(r.Dir), orNone(prev))
	if len(pruned) > 0 {
		fmt.Fprintf(out, "removed %d older runtime(s)\n", len(pruned))
	}
	return nil
}
