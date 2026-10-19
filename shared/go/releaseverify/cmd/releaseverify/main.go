// Command releaseverify verifies a Maktaba release directory offline with the
// same code every machine installs through (shared/go/releaseverify).
//
//	releaseverify --tag v1.5.0 --dir dist                 # release assets (release.yml self-check)
//	releaseverify --tag v1.5.0 --dir bin/releases/1.5.0 --installed
//
// --repo (default $MAKTABA_UPDATE_REPO, else Hamza-Labs-Core/Maktaba) selects the
// signing identity. Exit status 0 means verified; 1 prints the typed failure.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

func main() {
	var (
		tag       = flag.String("tag", "", "release tag (vX.Y.Z)")
		dir       = flag.String("dir", ".", "directory holding checksums.txt, checksums.txt.sigstore.json and release.json")
		repo      = flag.String("repo", os.Getenv(releaseverify.RepoEnv), "repository owner/name of the signing workflow")
		installed = flag.Bool("installed", false, "the directory is an extracted release: check its binaries against release.json")
		platform  = flag.String("platform", "", "os/arch for --installed (default: this machine)")
	)
	flag.Parse()
	id, err := releaseverify.NewIdentity(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	mode := releaseverify.ReleaseAssets
	if *installed {
		mode = releaseverify.InstalledRelease
	}
	res, err := releaseverify.VerifyDir(*dir, *tag, id, mode, *platform, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "releaseverify: FAILED:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "verified %s (%s), signed %s by %s, checksums.txt sha256 %s\n",
		res.Manifest.Tag, res.Manifest.Commit, res.SignedAt.Format("2006-01-02T15:04:05Z"), res.Identity, res.ChecksumsSHA256)
}
