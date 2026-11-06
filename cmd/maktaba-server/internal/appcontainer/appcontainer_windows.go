//go:build windows

package appcontainer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	userenv              = windows.NewLazySystemDLL("userenv.dll")
	procCreateProfile    = userenv.NewProc("CreateAppContainerProfile")
	procDeriveProfileSID = userenv.NewProc("DeriveAppContainerSidFromAppContainerName")
)

const (
	procThreadAttributeSecurityCapabilities = 0x00020009
	hresultAlreadyExists                    = 0x800700B7
	jobObjectLimitActiveProcess             = 0x00000008
)

// securityCapabilities is SECURITY_CAPABILITIES: an AppContainer SID and no capabilities.
type securityCapabilities struct {
	AppContainerSid *windows.SID
	Capabilities    uintptr
	CapabilityCount uint32
	Reserved        uint32
}

// SID creates the profile once (no capabilities) and answers its SID.
func SID(profile string) (*windows.SID, error) {
	name, err := windows.UTF16PtrFromString(profile)
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	hr, _, _ := procCreateProfile.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0, 0, uintptr(unsafe.Pointer(&sid)))
	if uint32(hr) == hresultAlreadyExists {
		hr, _, _ = procDeriveProfileSID.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&sid)))
	}
	if hr != 0 {
		return nil, fmt.Errorf("appcontainer: profile %s: HRESULT 0x%08x", profile, uint32(hr))
	}
	return sid, nil
}

// Grant gives the profile's SID read and execute (and, with write, full control) on a
// folder or file, inherited by what it holds.
func Grant(profile, path string, write bool) error {
	sid, err := SID(profile)
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("appcontainer: %s: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	mask := windows.ACCESS_MASK(windows.GENERIC_READ | windows.GENERIC_EXECUTE)
	if write {
		mask = windows.GENERIC_ALL
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: mask,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, dacl)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func envBlock(env []string) *uint16 {
	if env == nil {
		env = []string{"SystemRoot=" + os.Getenv("SystemRoot")}
	}
	// a block sorted by name, as Windows keeps its own
	env = append([]string(nil), env...)
	sort.Slice(env, func(i, j int) bool { return strings.ToUpper(env[i]) < strings.ToUpper(env[j]) })
	var b []uint16
	for _, kv := range env {
		u, err := windows.UTF16FromString(kv)
		if err != nil {
			continue
		}
		b = append(b, u...)
	}
	b = append(b, 0)
	return &b[0]
}

// Run starts exe in the profile's AppContainer, inside a Job Object (one process, no
// children, killed when the job closes, memory capped), with this process's stdout and
// stderr; it waits and answers the exit code.
func Run(profile, exe string, args []string, o Options) (int, error) {
	sid, err := SID(profile)
	if err != nil {
		return 0, err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return 0, err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("appcontainer: job object: %w", err)
	}
	defer func() { _ = windows.CloseHandle(job) }()
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | jobObjectLimitActiveProcess
	info.BasicLimitInformation.ActiveProcessLimit = 1
	if o.MemoryBytes > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
		info.ProcessMemoryLimit = uintptr(o.MemoryBytes)
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return 0, fmt.Errorf("appcontainer: job limits: %w", err)
	}
	stdout, stderr := windows.Handle(os.Stdout.Fd()), windows.Handle(os.Stderr.Fd())
	for _, h := range []windows.Handle{stdout, stderr} {
		_ = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return 0, err
	}
	defer attrs.Delete()
	caps := securityCapabilities{AppContainerSid: sid}
	if err := attrs.Update(procThreadAttributeSecurityCapabilities, unsafe.Pointer(&caps), unsafe.Sizeof(caps)); err != nil {
		return 0, fmt.Errorf("appcontainer: security capabilities: %w", err)
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdOutput, si.StdErr = stdout, stderr
	exe16, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 0, err
	}
	cmd16, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{exe}, args...)))
	if err != nil {
		return 0, err
	}
	var dir16 *uint16
	if o.Dir != "" {
		if dir16, err = windows.UTF16PtrFromString(o.Dir); err != nil {
			return 0, err
		}
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(exe16, cmd16, nil, nil, true, flags, envBlock(o.Env), dir16, &si.StartupInfo, &pi); err != nil {
		return 0, fmt.Errorf("appcontainer: start %s: %w", exe, err)
	}
	defer func() { _ = windows.CloseHandle(pi.Process); _ = windows.CloseHandle(pi.Thread) }()
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return 0, fmt.Errorf("appcontainer: assign to job: %w", err)
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return 0, err
	}
	if _, err := windows.WaitForSingleObject(pi.Process, windows.INFINITE); err != nil {
		return 0, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(pi.Process, &code); err != nil {
		return 0, err
	}
	return int(code), nil
}
