package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

// printVersion is `--version`: one line, or with --json the version, commit, build date
// and the front door's edge_protocol (Epic 35 design §7.3: an update compares it with the
// new release's to decide whether the front door restarts, without starting it).
func printVersion(w io.Writer, args []string) {
	if len(args) > 0 && args[0] == "--json" {
		_ = json.NewEncoder(w).Encode(map[string]any{"version": version.Version, "commit": version.Commit,
			"build_date": version.BuildDate, "edge_protocol": frontdoor.EdgeProtocol})
		return
	}
	fmt.Fprintln(w, version.String())
}
