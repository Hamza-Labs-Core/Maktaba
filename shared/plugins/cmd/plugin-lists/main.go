// Command plugin-lists fetches the plugin store's current store-keys.json and revoked.json
// for a release to embed (Epic 54, story 54.14 §3, issue #755): a home that never reaches
// the store still trusts store-signed files and knows the plugins the store withdrew.
//
//	go run ./cmd/plugin-lists [--cdn URL] [--dir DIR]
//
// It fails closed. Once the committed plugins-root.json lists keys, each list is fetched
// from the store's CDN, verified by the chain of trust (store-keys.json by the root,
// revoked.json by a store key), refused when expired or older than the copy already
// committed, and written into DIR (shared/plugins by default, where go:embed reads them).
// Any failure (the CDN unreachable, a bad signature, an expired or older list) exits 1 with
// an error naming it: a release never ships stale lists silently. While the root lists no
// keys (the store is not live), nothing a store signs could verify: it writes nothing,
// says so, and exits 0, and the committed files stay null.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	plugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// DefaultCDN is the store's CDN.
const DefaultCDN = "https://plugins-cdn.maktaba.tv"

func main() {
	cdn := flag.String("cdn", DefaultCDN, "the store's CDN")
	dir := flag.String("dir", "shared/plugins", "where the embedded lists are written")
	flag.Parse()
	msg, err := run(context.Background(), plugins.PluginsRoot, *cdn, *dir, &http.Client{Timeout: 60 * time.Second}, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "::error::plugin-lists: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(msg)
}

// run is the command, with its inputs given (tests).
func run(ctx context.Context, rootRaw []byte, cdn, dir string, c *http.Client, now time.Time) (string, error) {
	root, err := pluginsig.ParseRoot(rootRaw)
	if err != nil {
		return "", fmt.Errorf("the committed plugins-root.json: %w", err)
	}
	if root.Empty() {
		return "::notice::plugin-lists: the plugin root lists no keys (the store is not live): nothing to embed", nil
	}
	// the floor: the copies already committed (a release never embeds older ones)
	keysPath := filepath.Join(dir, "store-keys-embedded.json")
	revPath := filepath.Join(dir, "revoked-embedded.json")
	st := pluginsig.State{}
	if st.StoreKeysVersion, err = committedVersion(keysPath); err != nil {
		return "", err
	}
	if st.RevokedVersion, err = committedVersion(revPath); err != nil {
		return "", err
	}
	keysRaw, err := get(ctx, c, cdn, "store-keys.json")
	if err != nil {
		return "", err
	}
	keys, err := root.VerifyStoreKeys(keysRaw, st, now)
	if err != nil {
		return "", fmt.Errorf("store-keys.json from %s: %w", cdn, err)
	}
	revRaw, err := get(ctx, c, cdn, "revoked.json")
	if err != nil {
		return "", err
	}
	rev, err := keys.VerifyRevoked(revRaw, st, now)
	if err != nil {
		return "", fmt.Errorf("revoked.json from %s: %w", cdn, err)
	}
	if err := os.WriteFile(keysPath, keysRaw, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(revPath, revRaw, 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("plugin-lists: embedded store-keys.json version %d and revoked.json version %d (expires %s)",
		keys.Version, rev.Version, rev.ExpiresAt.UTC().Format(time.RFC3339)), nil
}

func get(ctx context.Context, c *http.Client, cdn, name string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cdn, "/")+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the store's CDN could not be reached for %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the store's CDN answered %d for %s", resp.StatusCode, name)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	return b, nil
}

// committedVersion is the version of a committed list (0 for null: none yet).
func committedVersion(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	t := strings.TrimSpace(string(b))
	if t == "" || t == "null" {
		return 0, nil
	}
	var env struct {
		Signed struct {
			Version int64 `json:"version"`
		} `json:"signed"`
	}
	if err := json.Unmarshal([]byte(t), &env); err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	if env.Signed.Version <= 0 {
		return 0, errors.New(path + ": no version")
	}
	return env.Signed.Version, nil
}
