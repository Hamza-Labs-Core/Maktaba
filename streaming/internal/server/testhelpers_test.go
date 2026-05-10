package server

import (
	"errors"
	"io"
	"os"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/handlers"
)

// fakeOpener is a minimal handlers.FileOpener for server_test.go.
type fakeOpener struct {
	file []byte
}

func (f *fakeOpener) Open(_ string) (handlers.FileReader, error) {
	if len(f.file) == 0 {
		return nil, errors.New("not found")
	}
	return &fakeFile{data: f.file}, nil
}

type fakeFile struct {
	data []byte
	pos  int64
}

func (f *fakeFile) Read(p []byte) (int, error) {
	if f.pos >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.pos:])
	f.pos += int64(n)
	return n, nil
}

func (f *fakeFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case 0:
		f.pos = offset
	case 1:
		f.pos += offset
	case 2:
		f.pos = int64(len(f.data)) + offset
	}
	return f.pos, nil
}

func (f *fakeFile) Close() error { return nil }

func (f *fakeFile) Stat() (os.FileInfo, error) {
	return fakeStat{name: "x", size: int64(len(f.data))}, nil
}

type fakeStat struct {
	name string
	size int64
}

func (s fakeStat) Name() string       { return s.name }
func (s fakeStat) Size() int64        { return s.size }
func (s fakeStat) Mode() os.FileMode  { return 0o644 }
func (s fakeStat) ModTime() time.Time { return time.Time{} }
func (s fakeStat) IsDir() bool        { return false }
func (s fakeStat) Sys() any           { return nil }
