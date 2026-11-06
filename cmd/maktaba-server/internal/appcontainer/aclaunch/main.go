// Command aclaunch runs a program inside the plugin host's Windows AppContainer and Job
// Object (story 54.03 §4) for the plugins-sandbox CI job:
//
//	aclaunch [-profile name] [-read dir]... [-write dir]... -- program args...
//
// It grants the profile read (-read) or full control (-write) on the folders, starts the
// program with this process's stdout and stderr, and exits with its exit code.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appcontainer"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	var reads, writes list
	profile := flag.String("profile", appcontainer.Profile, "the AppContainer profile")
	memory := flag.Uint64("memory", 2<<30, "the Job Object's memory cap in bytes")
	flag.Var(&reads, "read", "a folder the container may read (repeatable)")
	flag.Var(&writes, "write", "a folder the container may read and write (repeatable)")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "aclaunch: a program to run is required after --")
		os.Exit(2)
	}
	for _, d := range reads {
		if err := appcontainer.Grant(*profile, d, false); err != nil {
			fmt.Fprintln(os.Stderr, "aclaunch:", err)
			os.Exit(2)
		}
	}
	for _, d := range writes {
		if err := appcontainer.Grant(*profile, d, true); err != nil {
			fmt.Fprintln(os.Stderr, "aclaunch:", err)
			os.Exit(2)
		}
	}
	env := []string{"SystemRoot=" + os.Getenv("SystemRoot")}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PROBE_") || strings.HasPrefix(kv, "MAKTABA_PLUGIND_") {
			env = append(env, kv)
		}
	}
	code, err := appcontainer.Run(*profile, args[0], args[1:], appcontainer.Options{MemoryBytes: *memory, Env: env})
	if err != nil {
		fmt.Fprintln(os.Stderr, "aclaunch:", err)
		os.Exit(2)
	}
	os.Exit(code)
}
