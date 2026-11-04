package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// `maktaba-server release-offline` (Epic 51 story 51.17, hidden: the release's tool): the
// offline file the Linux maktaba-full package carries, made from a folder of the release's
// files (checksums.txt, its Sigstore bundle, release.json, components-<v>.json, and the
// packs under packs/) by 51.16's own MakeOffline over a FolderSource, so every file is
// verified against the release's signature and index as an install verifies it, and the
// import at first start (serve_systempacks.go) reads exactly what MakeOffline writes.
func newReleaseOfflineCmd() *cobra.Command {
	var dir, ver, platform, comps, out string
	cmd := &cobra.Command{
		Use:    "release-offline",
		Short:  "Make a release's offline file from its files (the Linux maktaba-full package)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			var list []string
			for _, s := range strings.Split(comps, ",") {
				if s = strings.TrimSpace(s); s != "" {
					list = append(list, s)
				}
			}
			items, err := releaseOffline(c.Context(), dir, strings.TrimPrefix(ver, "v"), platform, list, out)
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "%s: %s\n", out, strings.Join(items, ", "))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "release-dir", "", "the release's files (the signed set, the index, packs/<file>)")
	cmd.Flags().StringVar(&ver, "version", "", "the release's version")
	cmd.Flags().StringVar(&platform, "platform", "", "the packs' platform (linux-amd64, linux-arm64)")
	cmd.Flags().StringVar(&comps, "components", "runtime.ml,tool.yt-dlp", "the components whose assets it carries")
	cmd.Flags().StringVar(&out, "out", "", "the offline file to write")
	return cmd
}

// releaseOffline writes the offline file and returns the ids it carries; it refuses a
// file that would leave one out (a pack the release lacks for platform).
func releaseOffline(ctx context.Context, dir, ver, platform string, comps []string, out string) ([]string, error) {
	if dir == "" || ver == "" || platform == "" || out == "" || len(comps) == 0 {
		return nil, errors.New("--release-dir, --version, --platform, --components and --out are needed")
	}
	work, err := os.MkdirTemp("", "release-offline-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	id, err := rv.IdentityFromEnv()
	if err != nil {
		return nil, err
	}
	trust := &rv.RootStore{Dir: filepath.Join(work, "sigstore")}
	m := &packs.Manager{
		Layout:   packs.Layout{Root: filepath.Join(work, "components")},
		Catalog:  components.Default(),
		Version:  ver,
		Platform: platform,
		Net:      &packs.FolderSource{Dir: dir},
		Trusted: func(ctx context.Context) (root.TrustedMaterial, error) {
			if err := trust.Refresh(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "release-offline: the Sigstore trusted root was not refreshed (%v): the embedded one\n", err)
			}
			tm, _, err := trust.TrustedMaterial()
			return tm, err
		},
		Identity: id,
		Log:      os.Stderr,
	}
	req := packs.OfflineRequest{Components: comps, Platform: platform}
	want, err := m.OfflineItems(req)
	if err != nil {
		return nil, err
	}
	tmp := out + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	items, err := m.MakeOffline(ctx, f, req)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		var missing []string
		for _, w := range want {
			if !slices.Contains(items, w) {
				missing = append(missing, w)
			}
		}
		if len(missing) > 0 {
			err = fmt.Errorf("the release has no %s for %s", strings.Join(missing, ", "), platform)
		}
	}
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return items, os.Rename(tmp, out)
}
