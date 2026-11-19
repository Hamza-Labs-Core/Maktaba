// Command fp-packages checks a release directory's first-party plugin packages (Epic 54,
// story 54.27 §2, T01) the way the server trusts one (story 54.22 §2,
// firstparty.CheckPackage): every plugin of the bundle this binary embeds has its
// <id>-<version>.mkplugin and <id>-<version>.files.json, both listed in the directory's
// checksums.txt; each unpacks with the home's reader to that files.json; its manifest,
// strings and statements are the embedded ones, stamped with the version. It prints each
// package checked and exits 1 on the first that is not trusted.
//
//	fp-packages -dir dist -version 0.3.152
//
// release.yml runs it on the release's own dist/ after its checksums are signed and
// verified; CI runs it on a dry run of tools/plugins/build.py.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("fp-packages", flag.ContinueOnError)
	dir := fs.String("dir", "", "the release directory (holds checksums.txt)")
	version := fs.String("version", "", "the release version (a tag without its v)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || *version == "" {
		fmt.Fprintln(os.Stderr, "usage: fp-packages -dir <release dir> -version <version>")
		return 2
	}
	v := strings.TrimPrefix(*version, "v")
	raw, err := os.ReadFile(filepath.Join(*dir, releaseverify.ChecksumsFile))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fp-packages:", err)
		return 1
	}
	sums, err := releaseverify.ParseChecksums(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fp-packages:", err)
		return 1
	}
	names, err := firstparty.VerifyReleasePackages(*dir, v, sums)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fp-packages:", err)
		return 1
	}
	for _, n := range names {
		_, _ = fmt.Fprintln(os.Stdout, "trusted", n)
	}
	_, _ = fmt.Fprintf(os.Stdout, "%d first-party packages, all trusted\n", len(names))
	return 0
}
