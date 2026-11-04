// Command pin-models pins the catalog's models (story 51.15): every hf: source to a commit
// with each file's sha256 and size, every ollama: source to its manifest digest with each
// layer's. Run it in a normal pull request, then `make catalog-sync`:
//
//	go run ./cmd/pin-models --catalog ../../shared/components/catalog.toml [--update]
//
// --update moves each source to its newest revision (main, or the ollama tag); without it
// a pinned source keeps its revision and only its file list is written again. HF_TOKEN,
// when set, is used for a gated repo's small files (without it they are left out, and
// said so).
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/tools/release/pinmodels"
)

func main() {
	catalog := flag.String("catalog", "../../shared/components/catalog.toml", "the catalog file to pin in place")
	update := flag.Bool("update", false, "move every source to its newest revision")
	check := flag.Bool("check", false, "fail when pinning would change the file (write nothing)")
	flag.Parse()
	data, err := os.ReadFile(*catalog)
	if err != nil {
		fail(err)
	}
	c := &pinmodels.Client{HTTP: &http.Client{Timeout: 2 * time.Minute}, HF: "https://huggingface.co",
		Registry: "https://registry.ollama.ai", Token: os.Getenv("HF_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	out, results, err := c.PinCatalog(ctx, data, *update)
	if err != nil {
		fail(err)
	}
	fmt.Fprint(os.Stderr, pinmodels.Summary(results))
	if *check {
		if string(out) != string(data) {
			fail(fmt.Errorf("%s is not pinned as the sources are now: run pin-models", *catalog))
		}
		return
	}
	if err := os.WriteFile(*catalog, out, 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "pin-models:", err)
	os.Exit(1)
}
