// Command release-order places a tag among the published releases (package
// releaseorder), for the release workflows. The published tags come on stdin, one per
// line (`gh release list --exclude-drafts --json tagName --jq '.[].tagName'`).
//
//	release-order guard    --tag vX.Y.Z [--allow vA.B.C]   exit 1 (with why) when refused; prints maintenance=,
//	                                                        published=, latest= (true|false) and newest= (a tag)
//	release-order previous --tag vX.Y.Z                     prints the previous release's tag (or nothing)
//	release-order latest   --tag vX.Y.Z                     prints true|false
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/tools/release/releaseorder"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "release-order: guard|previous|latest --tag vX.Y.Z (published tags on stdin)")
		return 2
	}
	fs := flag.NewFlagSet("release-order "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	tag := fs.String("tag", "", "the tag being released")
	allow := fs.String("allow", "", "guard: a tag released out of order on purpose (a maintenance line)")
	if err := fs.Parse(args[1:]); err != nil || *tag == "" {
		return 2
	}
	var published []string
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			published = append(published, t)
		}
	}
	if err := sc.Err(); err != nil {
		_, _ = fmt.Fprintln(stderr, "release-order:", err)
		return 1
	}
	switch args[0] {
	case "guard":
		p, err := releaseorder.Locate(*tag, published, *allow)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "release-order: refused:", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "maintenance=%v\npublished=%v\nlatest=%v\nnewest=%s\n", p.Maintenance, p.Published,
			p.Latest, p.Newest)
	case "previous":
		_, _ = fmt.Fprintln(stdout, releaseorder.Previous(*tag, published))
	case "latest":
		_, _ = fmt.Fprintln(stdout, releaseorder.Latest(*tag, published))
	default:
		_, _ = fmt.Fprintf(stderr, "release-order: unknown command %q\n", args[0])
		return 2
	}
	return 0
}
