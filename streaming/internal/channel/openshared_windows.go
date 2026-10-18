//go:build windows

package channel

import (
	"os"
	"syscall"
)

// openShared opens a file for reading while allowing other processes to
// rename over or delete it (FILE_SHARE_DELETE). Without this, ffmpeg's
// HLS muxer fails to replace index.m3u8 / delete old segments while a
// player is downloading them, which aborts the channel muxer on Windows.
func openShared(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
