// Command broadcast-report prints the broadcast rehearsal's Markdown report (story 50.29 §3) from
// a folder of its result files (the CI artifacts of every scenario job), and exits 1 when any
// scenario failed or passed while listed as an expected failure.
//
//	go run ./cmd/broadcast-report <dir>...
//	go run ./cmd/broadcast-report -matrix pr        # the set's scenarios with a body, as JSON (the CI matrix)
//	go run ./cmd/broadcast-report -unbuilt nightly  # the others, as a -run pattern
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/tools/broadcast-viewers/rehearsal"
)

func main() {
	matrix := flag.String("matrix", "", "print the set's (pr, nightly) scenarios with a body as a JSON array")
	unbuilt := flag.String("unbuilt", "", "print the set's scenarios without a body as a go test -run pattern")
	flag.Parse()
	switch {
	case *matrix != "":
		raw, _ := json.Marshal(rehearsal.InSet(*matrix, true))
		fmt.Println(string(raw))
		return
	case *unbuilt != "":
		names := rehearsal.InSet(*unbuilt, false)
		if len(names) == 0 {
			return
		}
		fmt.Printf("TestBroadcast/(%s)$\n", strings.Join(names, "|"))
		return
	}
	var all []rehearsal.Result
	bad := 0
	for _, dir := range flag.Args() {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
				return nil
			}
			r, err := rehearsal.ReadResult(p)
			if err != nil || r.Scenario == "" {
				return nil
			}
			all = append(all, r)
			if r.Status.Counts() {
				bad++
			}
			return nil
		})
	}
	fmt.Print(rehearsal.Report(all))
	if len(all) == 0 {
		fmt.Fprintln(os.Stderr, "broadcast-report: no result found")
		os.Exit(1)
	}
	if bad > 0 {
		fmt.Fprintf(os.Stderr, "broadcast-report: %d scenario(s) failed or passed while listed\n", bad)
		os.Exit(1)
	}
}
