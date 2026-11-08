// Command maktaba-symbols makes the symbol tables of a release's dSYMs (and its Android apps'
// R8 mappings) and uploads them to Maktaba Cloud (Epic 53 stories 53.14 and 53.16), so the
// apps' crash frames in a forwarded problem report read as functions, files and lines:
//
//	maktaba-symbols extract [-o DIR] X.dSYM…                 one table per binary and architecture
//	maktaba-symbols upload [-url URL] [-version V] X.dSYM…   extract and upload them
//
// upload also takes the tables extract wrote (<debug_id>.symbols): a release that could not
// reach the cloud keeps them, and they are uploaded later as they are.
//
// With -r8 APP, each path is instead an Android release's R8 mapping (mapping.txt) of that
// app ("android.player", "android.remote", "android.tv": the app a report names), and
// -version is required: a mapping is found by its app and version, not by a UUID.
//
//	maktaba-symbols upload -r8 android.player -version 0.3.102 mapping.txt
//
// A path that is an ELF file is an Android app's unstripped native library (lib*.so before
// the release strips it): its table is keyed by the library's GNU build id, which the apps'
// native crashes carry.
//
//	maktaba-symbols extract -o tables lib/arm64-v8a/libfoo.so
//
// The upload's token is MAKTABA_SYMBOLS_TOKEN (the cloud's MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE).
// It runs where the dSYMs are; the cloud never sees the dSYM itself.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/symbols"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "maktaba-symbols:", err)
		os.Exit(1)
	}
}

const usage = "usage: maktaba-symbols extract [-o DIR] [-r8 APP -version V] X.dSYM|mapping.txt|lib.so… | " +
	"upload [-url URL] [-version V] [-r8 APP] X.dSYM|T.symbols|mapping.txt|lib.so…"

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	switch args[0] {
	case "extract":
		fs := flag.NewFlagSet("extract", flag.ContinueOnError)
		dir := fs.String("o", ".", "the directory the tables are written to (<debug_id>.symbols)")
		r8 := fs.String("r8", "", "the app the paths are R8 mappings of (android.player, …)")
		version := fs.String("version", "", "the release the mappings are of (with -r8)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return each(fs.Args(), *r8, *version, func(t *symbols.Table, b []byte) error {
			p := filepath.Join(*dir, t.DebugID+".symbols")
			if err := os.WriteFile(p, b, 0o644); err != nil {
				return err
			}
			if t.Kind == symbols.KindR8 {
				_, _ = fmt.Fprintf(out, "%s r8 %s %s %d classes → %s\n", t.DebugID, t.CodeFile, t.Release, len(t.Classes), p)
				return nil
			}
			if t.Kind == symbols.KindELF {
				_, _ = fmt.Fprintf(out, "%s %s %s build id %s %d functions → %s\n", t.DebugID, t.Arch, t.CodeFile, t.CodeID, len(t.Funcs), p)
				return nil
			}
			_, _ = fmt.Fprintf(out, "%s %s %s %d functions → %s\n", t.DebugID, t.Arch, t.CodeFile, len(t.Funcs), p)
			return nil
		})
	case "upload":
		fs := flag.NewFlagSet("upload", flag.ContinueOnError)
		base := fs.String("url", "https://api.maktaba.tv", "the cloud API")
		version := fs.String("version", "", "the release the dSYMs are of (shown beside each table; a mapping's key with -r8)")
		r8 := fs.String("r8", "", "the app the paths are R8 mappings of (android.player, …)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		token := strings.TrimSpace(os.Getenv("MAKTABA_SYMBOLS_TOKEN"))
		if token == "" {
			return fmt.Errorf("MAKTABA_SYMBOLS_TOKEN is not set")
		}
		u := strings.TrimRight(*base, "/") + "/v1/symbols"
		if *version != "" {
			u += "?version=" + url.QueryEscape(*version)
		}
		cl := &http.Client{Timeout: 5 * time.Minute}
		return each(fs.Args(), *r8, *version, func(t *symbols.Table, b []byte) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/gzip")
			resp, err := cl.Do(req)
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if resp.StatusCode != http.StatusCreated {
				return fmt.Errorf("%s %s: %s %s", t.CodeFile, t.Arch, resp.Status, strings.TrimSpace(string(msg)))
			}
			_, _ = fmt.Fprintf(out, "%s %s %s %s uploaded\n", t.DebugID, t.Arch, t.CodeFile, t.Release)
			return nil
		})
	}
	return fmt.Errorf("%s", usage)
}

// each extracts every path's tables and hands each, encoded, to do. A path ending in
// .symbols is a table extract already wrote, handed on as it is; an ELF file is a native
// library; with r8 set, every other path is that app's mapping of release version.
func each(paths []string, r8, version string, do func(*symbols.Table, []byte) error) error {
	if len(paths) == 0 {
		return fmt.Errorf("%s", usage)
	}
	if r8 != "" && strings.TrimPrefix(strings.TrimSpace(version), "v") == "" {
		return fmt.Errorf("-r8 needs -version: a mapping is found by its app and release")
	}
	for _, p := range paths {
		if r8 != "" && !strings.HasSuffix(p, ".symbols") {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			t, err := symbols.ParseR8(f, r8, version)
			_ = f.Close()
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			b, err := t.Encode()
			if err != nil {
				return err
			}
			if err := do(t, b); err != nil {
				return err
			}
			continue
		}
		if strings.HasSuffix(p, ".symbols") {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			t, err := symbols.Decode(b)
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			if err := do(t, b); err != nil {
				return err
			}
			continue
		}
		if symbols.IsELF(p) {
			t, err := symbols.ExtractELFPath(p)
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			b, err := t.Encode()
			if err != nil {
				return err
			}
			if err := do(t, b); err != nil {
				return err
			}
			continue
		}
		tables, err := symbols.ExtractPath(p)
		if err != nil {
			return err
		}
		for _, t := range tables {
			b, err := t.Encode()
			if err != nil {
				return err
			}
			if err := do(t, b); err != nil {
				return err
			}
		}
	}
	return nil
}
