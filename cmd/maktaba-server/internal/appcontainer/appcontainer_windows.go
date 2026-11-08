//go:build windows

package appcontainer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// securityCapabilities is SECURITY_CAPABILITIES: an AppContainer SID and its capabilities
// (none for the plugin host; internetClient for a runner group with net, story 54.23).
type securityCapabilities struct {
	AppContainerSid *windows.SID
	Capabilities    *windows.SIDAndAttributes
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

// SIDString is the profile's SID as a string (a runner's pipes admit it, story 54.23).
func SIDString(profile string) (string, error) {
	sid, err := SID(profile)
	if err != nil {
		return "", err
	}
	return sid.String(), nil
}

// fileRead and fileModify are the masks a grant holds: read and execute, or modify (read,
// write, execute and delete). Never full control: WRITE_DAC and WRITE_OWNER would let the
// AppContainer change the folder's ACL itself.
const (
	fileRead   = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE
	fileModify = fileRead | windows.FILE_GENERIC_WRITE | windows.DELETE
)

const inheritAll = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE

// hasGrant reports whether the DACL already has the SID's explicit allow entry with the
// inheritance flags asked and exactly mask (a grant made by an earlier start: no walk of
// the tree again).
func hasGrant(dacl *windows.ACL, sid *windows.SID, mask windows.ACCESS_MASK, flags uint8) bool {
	if dacl == nil {
		return false
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, uint32(i), &ace) != nil {
			return false
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			continue
		}
		const kinds = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE | windows.NO_PROPAGATE_INHERIT_ACE
		if ace.Header.AceFlags&kinds != flags {
			continue
		}
		s := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		// exactly mask: an entry with more (an earlier GENERIC_ALL, WRITE_DAC) is set anew
		if s.Equals(sid) && ace.Mask == mask {
			return true
		}
	}
	return false
}

// Revoke takes back every explicit entry of the profile's SID on a folder or file (and so
// what it inherited below): a runner's area it no longer has (story 54.23).
func Revoke(profile, path string) error {
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
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessMode: windows.REVOKE_ACCESS,
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

// Grant gives the profile's SID read and execute (and, with write, modify: never full
// control) on a folder or file, inherited by what it holds. A grant already there is left
// as it is.
func Grant(profile, path string, write bool) error {
	mask := windows.ACCESS_MASK(fileRead)
	if write {
		mask = fileModify
	}
	return grant(profile, path, mask, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT, inheritAll)
}

// GrantFiles gives the profile's SID read on a folder and the files directly in it, not
// its subfolders (a runner reads the worker's areas.json there, story 54.23; the other
// groups' run folders below are not its).
func GrantFiles(profile, path string) error {
	return grant(profile, path, fileRead, windows.SUB_OBJECTS_ONLY_INHERIT|windows.INHERIT_NO_PROPAGATE,
		windows.OBJECT_INHERIT_ACE|windows.NO_PROPAGATE_INHERIT_ACE)
}

func grant(profile, path string, mask windows.ACCESS_MASK, inheritance uint32, flags uint8) error {
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
	if hasGrant(dacl, sid, mask, flags) {
		return nil
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: mask,
		AccessMode:        windows.SET_ACCESS, // replaces the SID's entries: never more than mask
		Inheritance:       inheritance,
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

// profileVars are the variables Windows rewrites to an AppContainer's own folders when
// it starts one: CreateProcess fails (ERROR_ENVVAR_NOT_FOUND) when they are missing.
var profileVars = []string{"SystemRoot", "TEMP", "TMP", "LOCALAPPDATA", "APPDATA", "USERPROFILE"}

func envBlock(env []string) *uint16 {
	have := map[string]bool{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		have[strings.ToUpper(k)] = true
	}
	for _, k := range profileVars {
		if v := os.Getenv(k); v != "" && !have[strings.ToUpper(k)] {
			env = append(env, k+"="+v)
		}
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
	if o.ProcessLimit > 1 {
		info.BasicLimitInformation.ActiveProcessLimit = uint32(o.ProcessLimit)
	}
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
	var capSIDs []windows.SIDAndAttributes
	for _, c := range o.Capabilities {
		cs, err := windows.StringToSid(c)
		if err != nil {
			return 0, fmt.Errorf("appcontainer: capability %s: %w", c, err)
		}
		capSIDs = append(capSIDs, windows.SIDAndAttributes{Sid: cs, Attributes: windows.SE_GROUP_ENABLED})
	}
	if len(capSIDs) > 0 {
		caps.Capabilities, caps.CapabilityCount = &capSIDs[0], uint32(len(capSIDs))
	}
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
	runtime.KeepAlive(capSIDs)
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

// remotePath reports a folder on another machine: a UNC path, or a drive Windows says is
// remote (a mapped share).
func remotePath(p string) bool {
	if uncPath(p) {
		return true
	}
	vol := filepath.VolumeName(p)
	if vol == "" {
		return false
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(root) == windows.DRIVE_REMOTE
}
