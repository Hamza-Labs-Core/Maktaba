// Command update-trusted-root refreshes the Sigstore trusted root embedded in
// releaseverify (trusted_root.json + trusted_root.meta.json) from the public-good
// TUF repository, anchored on sigstore-go's embedded TUF root.
//
// Run it from the module directory:
//
//	go run ./cmd/update-trusted-root
//
// or `go generate ./...`. The release workflow refuses to build when the embedded
// root is older than 90 days (releaseverify's TestEmbeddedRootAge with
// MAKTABA_RELEASE_BUILD=1), so a maintainer runs this and commits the result.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "update-trusted-root:", err)
		os.Exit(1)
	}
}

func run() error {
	cache, err := os.MkdirTemp("", "maktaba-tuf-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(cache) }()

	opts := tuf.DefaultOptions().WithCachePath(cache)
	client, err := tuf.New(opts)
	if err != nil {
		return fmt.Errorf("tuf client: %w", err)
	}
	raw, err := client.GetTarget("trusted_root.json")
	if err != nil {
		return fmt.Errorf("fetch trusted_root.json: %w", err)
	}
	if _, err := root.NewTrustedRootFromJSON(raw); err != nil {
		return fmt.Errorf("parse trusted_root.json: %w", err)
	}
	meta, err := json.MarshalIndent(map[string]string{
		"fetched_at": time.Now().UTC().Format(time.RFC3339),
		"source":     opts.RepositoryBaseURL,
	}, "", "  ")
	if err != nil {
		return err
	}
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.WriteFile(filepath.Join(dir, "trusted_root.json"), raw, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "trusted_root.meta.json"), append(meta, '\n'), 0o644)
}
