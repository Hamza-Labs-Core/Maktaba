// Command aclaunch runs a program inside the plugin host's Windows AppContainer and Job
// Object (story 54.03 §4) for the plugins-sandbox CI job:
//
//	aclaunch [-profile name] [-memory bytes] [-read dir]... [-write dir]... -- program args...
//
// It grants the profile read (-read) or full control (-write) on the folders, starts the
// program with this process's stdout and stderr, and exits with its exit code. It is
// appcontainer.Launch, the code the supervisor runs as `maktaba-server plugins-sandbox-run`.
package main

import (
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appcontainer"
)

func main() {
	os.Exit(appcontainer.Launch(os.Args[1:], os.Stderr))
}
