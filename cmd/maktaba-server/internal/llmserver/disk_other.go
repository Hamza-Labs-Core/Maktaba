//go:build !unix

package llmserver

// FreeBytes is unknown (-1) here: the app is macOS only.
func FreeBytes(string) int64 { return -1 }
