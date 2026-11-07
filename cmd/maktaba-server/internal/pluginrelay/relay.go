// Package pluginrelay carries the pipeline's dispatch connections to the plugin host on
// Windows (#710).
//
// maktaba-plugind serves its dispatch socket as AF_UNIX on every OS, Windows included,
// and the supervisor's Go reaches it there. The pipeline cannot: Python on Windows has no
// AF_UNIX, and the host, inside its AppContainer (story 54.03 §4), may not create a named
// pipe. So on Windows the supervisor serves a named pipe for the pipeline and copies each
// connection, byte for byte in both directions, to the host's AF_UNIX socket:
//
//   - the pipe's name is random for each run of the supervisor (EnvPipe tells the pipeline);
//   - its DACL admits the user the supervisor runs as, no one else;
//   - it refuses remote clients (PIPE_REJECT_REMOTE_CLIENTS), and the first instance is
//     created with FILE_FLAG_FIRST_PIPE_INSTANCE: a name someone else made first is not
//     served (no squatting);
//   - the relay answers each client with Ready once it has reached the host, and closes
//     the client without it when it cannot: the pipeline then raises the error a refused
//     unix connection raises, so the worker falls back the same way on every OS.
//
// The host's dispatch protocol is unchanged: after Ready the relay is transparent.
package pluginrelay

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sync"
)

// EnvPipe names the relay's pipe for the pipeline (\\.\pipe\maktaba-plugind-<random>).
const EnvPipe = "MAKTABA_PLUGIND_PIPE"

// Ready is the one byte the relay sends a client once the host's socket answered.
const Ready byte = 0x01

// ErrUnsupported: the relay is Windows only.
var ErrUnsupported = errors.New("pluginrelay: the named pipe relay is Windows only")

// PipeName is a new pipe name: 128 random bits, never reused.
func PipeName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return `\\.\pipe\maktaba-plugind-` + hex.EncodeToString(b[:]), nil
}

// Pump copies a to b and b to a until either side ends, then closes both. A write blocks
// until the other side reads (the copy holds no more than its buffer), so a slow reader
// slows the writer down: backpressure, both ways.
func Pump(a, b io.ReadWriteCloser) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = a.Close()
			_ = b.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		closeBoth()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		closeBoth()
	}()
	wg.Wait()
}
