// Command mkplugin packs a plugin folder into a .mkplugin (Epic 54, story 54.01 §1) with
// the same writer the tests use (componentpack.BuildPlugin): manifest.toml and files.json
// first, every file's sha256 listed. It signs nothing (54.14).
//
//	mkplugin <folder> <out.mkplugin>
package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mkplugin <folder> <out.mkplugin>")
		os.Exit(2)
	}
	dir, out := os.Args[1], os.Args[2]
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkplugin:", err)
		os.Exit(1)
	}
	var buf bytes.Buffer
	sum, err := componentpack.BuildPlugin(&buf, files, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkplugin:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "mkplugin:", err)
		os.Exit(1)
	}
	fmt.Println(sum)
}
