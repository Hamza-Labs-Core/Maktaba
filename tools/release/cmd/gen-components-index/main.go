// Command gen-components-index writes a release's component index (story 51.15), or lists
// the packs a platform builds:
//
//	gen-components-index --list --platform linux-amd64 [--catalog FILE]
//	gen-components-index --version 0.4.2 --commit <sha> --published <RFC 3339> --dist dist [--catalog FILE]
//	    [--platform P [--only ID]]
//
// The index goes to dist/components-<version>.json. Every pack the catalog asks for must be
// in dist: a missing one (a failed build leg) fails the command and writes no index, so a
// release never ships without some platform's packs. --platform and --only index part of a
// release (one platform's packs, the update rehearsal's tool.yt-dlp): a release sets neither.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/tools/release/componentsindex"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen-components-index:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		list      = flag.Bool("list", false, "print the packs --platform builds (id per line) and stop")
		platform  = flag.String("platform", "", "the platform for --list; for an index, only its packs")
		only      = flag.String("only", "", "for an index, only this pack (needs --platform)")
		catalog   = flag.String("catalog", "", "a catalog file (default: the embedded one)")
		version   = flag.String("version", "", "the release version, without v")
		commit    = flag.String("commit", "", "the 40-hex commit")
		published = flag.String("published", "", "the commit's time, RFC 3339")
		dist      = flag.String("dist", "dist", "the release files")
	)
	flag.Parse()
	cat := components.Default()
	if *catalog != "" {
		b, err := os.ReadFile(*catalog)
		if err != nil {
			return err
		}
		if cat, err = components.Load(b); err != nil {
			return err
		}
	}
	if *list {
		if *platform == "" {
			return fmt.Errorf("--list needs --platform")
		}
		for _, p := range componentsindex.Packs(cat, *platform) {
			fmt.Println(p.ID)
		}
		return nil
	}
	pub, err := time.Parse(time.RFC3339, *published)
	if err != nil {
		return fmt.Errorf("--published: %w", err)
	}
	o := componentsindex.Options{Catalog: cat, Version: *version, Commit: *commit, Published: pub, Dist: *dist,
		Platform: *platform}
	if *only != "" {
		if *platform == "" {
			return fmt.Errorf("--only needs --platform")
		}
		o.Only = []string{*only}
	}
	idx, err := componentsindex.Generate(o)
	if err != nil {
		return err
	}
	b, err := componentsindex.Marshal(idx)
	if err != nil {
		return err
	}
	out := filepath.Join(*dist, rv.ComponentsIndexName(*version))
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s: %d packs, %d model sources\n", out, len(idx.Packs), len(idx.Models))
	return nil
}
