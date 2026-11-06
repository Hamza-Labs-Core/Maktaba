// Package appcontainer starts a process inside a Windows AppContainer with no
// capabilities and inside a Job Object (Epic 54, story 54.03 §4): the plugin host
// (maktaba-plugind) runs only there. An AppContainer without internetClient,
// internetClientServer or privateNetworkClientServer has every network path blocked by the
// Windows Filtering Platform, and reads and writes only what folder ACLs grant its SID.
//
// The supervisor's plugins role uses it (story 54.07); `aclaunch` is the same code as a
// command for the plugins-sandbox CI job. On other systems every call answers
// ErrUnsupported.
package appcontainer

import "errors"

// Profile is the AppContainer the plugin host runs in.
const Profile = "Maktaba.Plugins"

// ErrUnsupported: not Windows.
var ErrUnsupported = errors.New("appcontainer: Windows only")

// Options of a start.
type Options struct {
	// MemoryBytes caps the process's committed memory (0: no cap).
	MemoryBytes uint64
	// Env is the child's environment ("K=V"); SystemRoot, TEMP, TMP and the profile's
	// folders are added from this process's own (Windows needs them).
	Env []string
	// Dir is the working folder ("" for the caller's).
	Dir string
}
