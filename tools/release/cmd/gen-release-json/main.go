// Command gen-release-json writes release.json for a release (Epic 35 design
// §3.1) and checks one against the repository (story 35.1 AC2).
//
//	gen-release-json --tag v1.5.0 --commit <sha> --dist dist --repo . [--previous prev.json] > dist/release.json
//	gen-release-json --check dist/release.json --repo . [--previous prev.json]
//
// It fails when the highest migration slot, SCHEMA_REV and SchemaRev disagree,
// or (with --check) when release.json's schema_rev, min_node_schema,
// breaks_previous_home or protocol numbers disagree with the checkout.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/tools/release/genrelease"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen-release-json:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		tag       = flag.String("tag", "", "release tag (vX.Y.Z)")
		commit    = flag.String("commit", "", "40-hex commit the release was built from")
		channel   = flag.String("channel", "", "stable|beta (default: from the tag)")
		published = flag.String("published", "", "RFC 3339 publish time (default: now)")
		dist      = flag.String("dist", "dist", "directory with the built release assets")
		repo      = flag.String("repo", ".", "repository checkout root")
		previous  = flag.String("previous", "", "the previous release's release.json, if any")
		minUp     = flag.String("min-upgrade-from", "", "override min_upgrade_from")
		launcher  = flag.Int("launcher-protocol", 0, "launcher protocol (default 1)")
		check     = flag.String("check", "", "check this release.json against the repository instead of writing one")
	)
	flag.Parse()

	var prev *rv.Manifest
	if *previous != "" {
		b, err := os.ReadFile(*previous)
		if err != nil {
			return err
		}
		if prev, err = rv.ParseManifest(b); err != nil {
			return fmt.Errorf("--previous: %w", err)
		}
	}
	prevRev := 0
	if prev != nil {
		prevRev = prev.SchemaRev
	}

	if *check != "" {
		b, err := os.ReadFile(*check)
		if err != nil {
			return err
		}
		m, err := rv.ParseManifest(b)
		if err != nil {
			return err
		}
		if err := genrelease.Check(m, *repo, prevRev); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "release.json %s: schema_rev %d, min_node_schema %d, breaks_previous_home %v: consistent with the repository\n",
			m.Tag, m.SchemaRev, m.MinNodeSchema, m.BreaksPreviousHome)
		return nil
	}

	var pub time.Time
	if *published != "" {
		t, err := time.Parse(time.RFC3339, *published)
		if err != nil {
			return fmt.Errorf("--published: %w", err)
		}
		pub = t
	}
	m, err := genrelease.Build(genrelease.Options{
		Tag: *tag, Commit: *commit, Channel: *channel, Published: pub, Dist: *dist, Repo: *repo,
		LauncherProtocol: *launcher, Previous: prev, MinUpgradeFrom: *minUp,
	})
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(out, '\n'))
	return err
}
