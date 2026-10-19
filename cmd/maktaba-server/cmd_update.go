package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/selfupdate"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

func newUpdateCmd() *cobra.Command {
	var (
		check    bool
		target   string
		channel  string
		rollback bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Self-update to the latest signed release",
		Long: `update finds the latest release of this repository on GitHub Releases
(MAKTABA_UPDATE_REPO for a fork), verifies it in process — the Sigstore
bundle over checksums.txt signed by the release workflow of exactly that
tag, its Rekor inclusion proof, release.json, the archive and the
extracted binary — refuses anything older than the highest version this
machine ever installed, and atomically replaces this executable, keeping
the previous one as <binary>.bak.

--check only reports. --version installs one specific release (it must
still verify and must not be below the watermark). --rollback restores
<binary>.bak, the release that ran before the last update: the only way to
run a lower version, and it does not lower the watermark.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			self, err := selfupdate.Self()
			if err != nil {
				return err
			}
			if rollback {
				if err := selfupdate.Rollback(self); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Restored the previous release into %s. Restart the service to run it.\n", self)
				return nil
			}
			id, err := releaseverify.IdentityFromEnv()
			if err != nil {
				return err
			}
			if channel == "" {
				channel = os.Getenv("MAKTABA_UPDATE_CHANNEL")
			}
			c := selfupdate.New(selfupdate.Options{
				Identity: id, Channel: channel, Token: os.Getenv("MAKTABA_GITHUB_TOKEN"),
			})
			return runUpdate(cmd.Context(), cmd, c, self, version.Version, target, check)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "dry-run: only report whether an update is available")
	cmd.Flags().StringVar(&target, "version", "", "install this release (vX.Y.Z) instead of the latest")
	cmd.Flags().StringVar(&channel, "channel", "", "stable|beta (default $MAKTABA_UPDATE_CHANNEL, else stable)")
	cmd.Flags().BoolVar(&rollback, "rollback", false, "restore the release kept from before the last update")
	return cmd
}

func runUpdate(ctx context.Context, cmd *cobra.Command, c *selfupdate.Client, self, current, target string, check bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	out := cmd.OutOrStdout()
	var (
		rel *selfupdate.Release
		err error
	)
	if target != "" {
		rel, err = c.ByTag(ctx, target)
	} else {
		rel, err = c.Latest(ctx)
	}
	if errors.Is(err, selfupdate.ErrNoRelease) {
		fmt.Fprintln(out, "No matching release found.")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "current: %s\n", current)
	fmt.Fprintf(out, "release: %s\n", rel.Tag)
	if target == "" && !selfupdate.Newer(rel.Tag, current) {
		fmt.Fprintln(out, "You are up to date.")
		return nil
	}
	fmt.Fprintln(out, "Downloading and verifying ...")
	p, err := c.Prepare(ctx, rel, self)
	if err != nil {
		return fmt.Errorf("refusing %s: %w", rel.Tag, err)
	}
	fmt.Fprintf(out, "Verified %s: signed by %s at %s.\n", rel.Tag, p.Update.Result.Identity,
		p.Update.Result.SignedAt.Format("2006-01-02 15:04:05Z"))
	if check {
		fmt.Fprintln(out, "(--check) not installing. Re-run without --check to upgrade.")
		return nil
	}
	if err := p.Install(); err != nil {
		return err
	}
	fmt.Fprintf(out, "Updated %s to %s. Restart the service to run the new version.\n", self, rel.Tag)
	return nil
}
