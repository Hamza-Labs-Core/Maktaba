//go:build windows

package updater

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The app the server opens (OpenURL) must outlive the launcher's kill-on-close job,
// which the launcher closes after every run of the server (review B-H1): a child
// started detached from a process in such a job keeps running when the job closes.
func TestDetachedChildOutlivesTheJob(t *testing.T) {
	switch os.Getenv("MAKTABA_DETACH_HELPER") {
	case "parent": // inside the job: start the child detached once told to, say its pid
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		c := exec.Command(os.Args[0], "-test.run=^TestDetachedChildOutlivesTheJob$")
		c.Env = append(os.Environ(), "MAKTABA_DETACH_HELPER=child")
		detach(c)
		if err := c.Start(); err != nil {
			fmt.Println("error", err)
			return
		}
		fmt.Println(c.Process.Pid)
		time.Sleep(time.Minute)
		return
	case "child":
		time.Sleep(20 * time.Second)
		return
	}
	if testing.Short() {
		t.Skip("starts processes in a job: the integration tier")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(os.Args[0], "-test.run=^TestDetachedChildOutlivesTheJob$")
	parent.Env = append(os.Environ(), "MAKTABA_DETACH_HELPER=parent")
	in, _ := parent.StdinPipe()
	out, _ := parent.StdoutPipe()
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill() }()
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(parent.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.AssignProcessToJobObject(job, ph); err != nil {
		t.Fatal(err)
	}
	_ = windows.CloseHandle(ph)
	_, _ = in.Write([]byte("go\n"))
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("no pid from the parent: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parent said %q", line)
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE,
		false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.TerminateProcess(child, 0); _ = windows.CloseHandle(child) }()
	_ = windows.CloseHandle(job) // the launcher's job closes: its processes die
	if _, err := parent.Process.Wait(); err != nil {
		t.Fatal(err)
	}
	if ev, _ := windows.WaitForSingleObject(child, 500); ev == windows.WAIT_OBJECT_0 {
		t.Fatal("the detached child died with the job")
	}
}

func TestOpenedAppGetsNoServerEnvironment(t *testing.T) {
	got := withoutServerEnv([]string{"PATH=x", "MAKTABA_LAUNCHER_ROOT=y", "maktaba_app_bundle=z", "HOME=h"})
	if strings.Join(got, ",") != "PATH=x,HOME=h" {
		t.Fatalf("env %v", got)
	}
}
