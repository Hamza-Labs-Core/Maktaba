//go:build !unix

package ffmpeg

func nodeGroup(string) int { return -1 }
