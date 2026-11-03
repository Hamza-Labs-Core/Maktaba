// Command build-pack builds one component pack (story 51.15), or checks one:
//
//	SOURCE_DATE_EPOCH=<commit time> build-pack --dir STAGE --id runtime.ml --version 0.4.2 --platform linux-amd64 --out OUT [--part-size BYTES]
//	build-pack --verify NAME --out OUT
//
// --verify unpacks the pack (or its parts) into a temporary folder with the same rules a
// machine uses, and removes it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "build-pack:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dir      = flag.String("dir", "", "the tree to pack")
		id       = flag.String("id", "", "the asset component")
		version  = flag.String("version", "", "the release version, without v")
		platform = flag.String("platform", "", "darwin-arm64, windows-amd64, linux-amd64 or linux-arm64")
		out      = flag.String("out", "", "the folder the pack is written to (or read from, with --verify)")
		partSize = flag.Int64("part-size", 0, "split a pack over this many bytes (default 1.9 GiB)")
		verify   = flag.String("verify", "", "a pack's name in --out to check")
	)
	flag.Parse()
	if *verify != "" {
		r, _, closeAll, err := componentpack.Open(*out, *verify)
		if err != nil {
			return err
		}
		defer func() { _ = closeAll() }()
		tmp, err := os.MkdirTemp("", "pack-verify-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		m, err := componentpack.Unpack(r, filepath.Join(tmp, "x"), componentpack.Expect{})
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s %s %s, %d bytes unpacked, files.sha256 %s\n", *verify, m.ID, m.Version, m.Platform, m.UnpackedBytes, m.FilesSHA256)
		return nil
	}
	sde, err := strconv.ParseInt(os.Getenv("SOURCE_DATE_EPOCH"), 10, 64)
	if err != nil {
		return fmt.Errorf("SOURCE_DATE_EPOCH (the commit's time) is needed: %v", err)
	}
	b, err := componentpack.Build(componentpack.Options{Dir: *dir, ID: *id, Version: *version, Platform: *platform,
		ModTime: time.Unix(sde, 0), Out: *out, PartSize: *partSize})
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d bytes, sha256 %s, %d parts, %d bytes unpacked\n", b.Name, b.Size, b.SHA256, len(b.Parts), b.Manifest.UnpackedBytes)
	return nil
}
