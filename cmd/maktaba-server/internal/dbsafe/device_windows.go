//go:build windows

package dbsafe

import (
	"os"
	"path/filepath"
)

// deviceOf names the disk a folder is on (its volume).
func deviceOf(path string, _ os.FileInfo) string { return filepath.VolumeName(path) }
