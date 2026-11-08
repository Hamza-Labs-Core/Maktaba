//go:build !windows

package appcontainer

// Grant gives the profile's SID read (and write) on a folder and what it holds.
func Grant(_, _ string, _ bool) error { return ErrUnsupported }

// Run starts exe in the profile's AppContainer and Job Object with this process's
// stdout and stderr, waits, and answers its exit code.
func Run(_, _ string, _ []string, _ Options) (int, error) { return 0, ErrUnsupported }

// Revoke takes back every grant of the profile's SID on a folder and what it holds.
func Revoke(_, _ string) error { return ErrUnsupported }

// remotePath is a UNC path (elsewhere there is no drive type to ask).
func remotePath(p string) bool { return uncPath(p) }

// GrantFiles gives the profile's SID read on a folder and the files directly in it.
func GrantFiles(_, _ string) error { return ErrUnsupported }

// startBroker serves a runner's share folders (Windows only).
func startBroker(_ string, _ BrokerRoots) ([]string, func(uint32, uintptr), func(), error) {
	return nil, nil, nil, ErrUnsupported
}

// shareLocalPath maps a share this machine serves to its folder (Windows only).
func shareLocalPath(string) (string, bool, error) { return "", false, nil }
