package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/selfupdate"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
)

func newUpdateCmd() *cobra.Command {
	var (
		check       bool
		manifestURL string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Self-update to the latest release",
		Long: `update fetches the release manifest, compares it to the running
version, and — if newer — downloads the platform binary, verifies its
checksum, and atomically replaces this executable.

Use --check for a dry-run that only reports whether an update exists.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			m, err := selfupdate.FetchManifest(manifestURL)
			if err != nil {
				return err
			}
			d := selfupdate.Check(version.Version, m)

			fmt.Printf("current: %s\n", d.Current)
			fmt.Printf("latest:  %s\n", d.Latest)

			if !d.Available {
				fmt.Println("You are up to date (or no build exists for this platform).")
				return nil
			}
			fmt.Printf("An update is available for %s.\n", d.PlatformKey)
			if check {
				fmt.Println("(--check) not installing. Re-run without --check to upgrade.")
				return nil
			}

			fmt.Println("Downloading and verifying ...")
			path, err := selfupdate.Apply(d)
			if err != nil {
				return err
			}
			fmt.Printf("Updated %s to %s. Restart the service to run the new version.\n", path, d.Latest)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "dry-run: only report whether an update is available")
	cmd.Flags().StringVar(&manifestURL, "manifest-url", selfupdate.DefaultManifestURL, "release manifest URL")
	return cmd
}
