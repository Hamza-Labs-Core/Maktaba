// Command fp-sign writes the developer.sig of one of Maktaba's own plugin packages (Epic 54,
// story 54.45 §2): Maktaba's developer key signs the package's files.json, as `mkp pack
// --key` does for a third-party plugin. The key is the 32-byte seed in base64, the form
// `mkp keys new` keeps (<id>.key).
//
//	fp-sign -key <file> -plugin <id> -version <v> -files-json <files.json> -out <developer.sig>
//
// It prints the key's id. tools/plugins/build.py --developer-key runs it for every package;
// the signature does not change files.json, so the release's signed checksums do not move.
package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("fp-sign", flag.ContinueOnError)
	keyFile := fs.String("key", "", "the developer key: its seed in base64")
	id := fs.String("plugin", "", "the plugin's id")
	version := fs.String("version", "", "the package's version")
	filesJSON := fs.String("files-json", "", "the package's files.json")
	out := fs.String("out", "", "where to write developer.sig")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *keyFile == "" || *id == "" || *version == "" || *filesJSON == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: fp-sign -key <file> -plugin <id> -version <v> -files-json <files.json> -out <developer.sig>")
		return 2
	}
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fp-sign:", err)
		return 1
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != 32 {
		fmt.Fprintln(os.Stderr, "fp-sign: the key is not a 32-byte seed in base64")
		return 1
	}
	s := pluginsig.NewSigner(seed)
	fj, err := os.ReadFile(*filesJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fp-sign:", err)
		return 1
	}
	sig, err := pluginsig.SignDeveloper(s, *id, *version, fj)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fp-sign:", err)
		return 1
	}
	if err := os.WriteFile(*out, sig, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "fp-sign:", err)
		return 1
	}
	_, _ = fmt.Fprintln(os.Stdout, s.ID)
	return 0
}
