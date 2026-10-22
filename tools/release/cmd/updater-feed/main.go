// Command updater-feed writes latest.json, the desktop app's update feed, for the
// signed bundles desktop-release.yml staged (see package updaterfeed).
//
//	updater-feed --dir staged --repo owner/name --tag v1.5.0 [--notes TEXT] [--out latest.json]
package main

import (
	"os"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/tools/release/updaterfeed"
)

func main() {
	os.Exit(updaterfeed.Main(os.Args[1:], os.Stdout, os.Stderr, time.Now()))
}
