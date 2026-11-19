// Command mkplugin packs a plugin folder into a .mkplugin (Epic 54, story 54.01 §1) with
// the same writer the tests use (componentpack.BuildPlugin): manifest.toml and files.json
// first, every file's sha256 listed. It signs nothing (54.14).
//
//	mkplugin [-files-json <out.files.json>] <folder> <out.mkplugin>
//
// -files-json also writes the package's files.json beside it: a release lists that file in
// its signed checksums.txt, which is what makes a first-party package trusted (story 54.27
// §2, firstparty.CheckPackage).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
)

func main() {
	filesJSON := flag.String("files-json", "", "also write the package's files.json to this path")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: mkplugin [-files-json <out.files.json>] <folder> <out.mkplugin>")
		os.Exit(2)
	}
	dir, out := flag.Arg(0), flag.Arg(1)
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
	if *filesJSON != "" {
		// read back with the home's own reader: the files.json written is the one it reads
		fj, err := readFilesJSON(buf.Bytes())
		if err != nil {
			fmt.Fprintln(os.Stderr, "mkplugin:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*filesJSON, fj, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "mkplugin:", err)
			os.Exit(1)
		}
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "mkplugin:", err)
		os.Exit(1)
	}
	fmt.Println(sum)
}

// readFilesJSON unpacks the package into a scratch folder with componentpack.UnpackPlugin
// and answers its files.json.
func readFilesJSON(pkg []byte) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "mkplugin-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	p, err := componentpack.UnpackPlugin(bytes.NewReader(pkg), filepath.Join(tmp, "p"), componentpack.PluginExpect{})
	if err != nil {
		return nil, err
	}
	return p.FilesJSON, nil
}
