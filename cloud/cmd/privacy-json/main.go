// Command privacy-json prints the relay's privacy policy, privacy.CurrentPolicy(), as JSON: the
// policy the relay serves at GET /privacy. maktaba.tv's build (web/packages/website,
// scripts/legal.mjs, Epic 60 story 60.07) reads it to write the privacy page's cloud section,
// so the page and the relay cannot disagree.
//
//	go run ./cmd/privacy-json      (from cloud/)
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
)

// write prints the policy as indented JSON, one trailing newline.
func write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(privacy.CurrentPolicy())
}

func main() {
	if err := write(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "privacy-json:", err)
		os.Exit(1)
	}
}
