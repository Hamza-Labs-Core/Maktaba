// Command release-feed writes, verifies and watches the release feed (package
// releasefeed; update-feed.yml runs it).
package main

import (
	"os"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/tools/release/releasefeed"
)

func main() {
	os.Exit(releasefeed.Main(os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC()))
}
