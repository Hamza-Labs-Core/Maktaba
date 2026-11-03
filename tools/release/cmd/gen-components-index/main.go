// Command gen-components-index writes a release's component index (story 51.15), or lists
// the packs a platform builds:
//
//	gen-components-index --list --platform linux-amd64 [--catalog FILE]
//	gen-components-index --version 0.4.2 --commit <sha> --published <RFC 3339> --dist dist [--catalog FILE] [--require-all]
//
// The index goes to dist/components-<version>.json. A pack the catalog asks for that is
// not in dist (a failed build leg) is a warning, or an error with --require-all.
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
		list       = flag.Bool("list", false, "print the packs --platform builds (id per line) and stop")
		platform   = flag.String("platform", "", "the platform for --list")
		catalog    = flag.String("catalog", "", "a catalog file (default: the embedded one)")
		version    = flag.String("version", "", "the release version, without v")
		commit     = flag.String("commit", "", "the 40-hex commit")
		published  = flag.String("published", "", "the commit's time, RFC 3339")
		dist       = flag.String("dist", "dist", "the release files")
		requireAll = flag.Bool("require-all", false, "fail when a pack is missing")
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
	idx, missing, err := componentsindex.Generate(componentsindex.Options{Catalog: cat, Version: *version,
		Commit: *commit, Published: pub, Dist: *dist})
	if err != nil {
		return err
	}
	for _, m := range missing {
		prefix := ""
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			prefix = "::warning::"
		}
		fmt.Fprintf(os.Stderr, "%sno pack %s in %s: the index leaves it out\n", prefix, m, *dist)
	}
	if *requireAll && len(missing) > 0 {
		return fmt.Errorf("%d packs missing", len(missing))
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
