// Package migrations exposes the embedded SQL files as an embed.FS so
// the migrator can run them at startup without a separate file mount.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
