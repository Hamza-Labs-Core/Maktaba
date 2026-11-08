package appcontainer

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Launch runs a program inside the profile's AppContainer and Job Object, after granting
// the profile the folders named, and answers its exit code:
//
//	[-profile name] [-memory bytes] [-read dir]... [-write dir]... -- program args...
//
// The supervisor starts the plugin host this way on Windows (`maktaba-server
// plugins-sandbox-run`, #710), and the plugins-sandbox CI job's `aclaunch` is the same code.
// The program sees this process's environment entries named MAKTABA_PLUGIND_* or PROBE_*,
// and the ones Run adds (SystemRoot, TEMP and the profile's folders), nothing else.
func Launch(args []string, stderr io.Writer) int {
	// a first-party runner (story 54.23): its policy, its own profile (LaunchRunner)
	if len(args) > 0 && args[0] == "runner-sandbox-run" {
		return LaunchRunner(args, stderr)
	}
	// the supervisor names the subcommand first; aclaunch does not
	if len(args) > 0 && args[0] == "plugins-sandbox-run" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("plugins-sandbox-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var reads, writes list
	profile := fs.String("profile", Profile, "the AppContainer profile")
	memory := fs.Uint64("memory", 2<<30, "the Job Object's memory cap in bytes")
	fs.Var(&reads, "read", "a folder the container may read (repeatable)")
	fs.Var(&writes, "write", "a folder the container may read and write (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "plugins-sandbox-run: a program to run is required after --")
		return 2
	}
	// granted as the filesystem names them (short names expanded, links above resolved): a
	// grant applies only to the object its path names (Grant checks it by handle). A folder
	// that is itself a link or junction is refused, never its target granted
	grant := func(d string, write bool) error {
		if info, err := os.Lstat(d); err == nil && info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("%s is a link: never granted", d)
		}
		c, err := canonical(d)
		if err != nil {
			return err
		}
		return grantFn(*profile, c, write)
	}
	for _, d := range reads {
		if err := grant(d, false); err != nil {
			fmt.Fprintln(stderr, "plugins-sandbox-run:", err)
			return 2
		}
	}
	for _, d := range writes {
		if err := os.MkdirAll(d, 0o700); err != nil {
			fmt.Fprintln(stderr, "plugins-sandbox-run:", err)
			return 2
		}
		if err := grant(d, true); err != nil {
			fmt.Fprintln(stderr, "plugins-sandbox-run:", err)
			return 2
		}
	}
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PROBE_") || strings.HasPrefix(kv, "MAKTABA_PLUGIND_") {
			env = append(env, kv)
		}
	}
	code, err := runFn(*profile, rest[0], rest[1:], Options{MemoryBytes: *memory, Env: env})
	if err != nil {
		fmt.Fprintln(stderr, "plugins-sandbox-run:", err)
		return 2
	}
	return code
}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }
