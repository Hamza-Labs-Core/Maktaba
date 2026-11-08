//go:build windows

package appcontainer

import (
	"encoding/binary"
	"errors"
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

// Revoke takes back every explicit entry of the profile's SID on a folder or file, and
// what it passed down to what the folder holds: a runner's area it no longer has (story
// 54.23). Like Grant it changes only the object that was checked (openChecked).
func Revoke(profile, path string) error {
	sid, err := SID(profile)
	if err != nil {
		return err
	}
	return change(sid, path, nil, true)
}

// Grant gives the profile's SID read and execute (and, with write, modify: never full
// control) on a folder or file, inherited by what it holds. A grant already there is left
// as it is.
func Grant(profile, path string, write bool) error {
	mask := windows.ACCESS_MASK(fileRead)
	if write {
		mask = fileModify
	}
	sid, err := SID(profile)
	if err != nil {
		return err
	}
	return change(sid, path, &ourACE{mask: uint32(mask), flags: inheritAll}, false)
}

// GrantFiles gives the profile's SID read on a folder and the files directly in it, not
// its subfolders (a runner reads the worker's areas.json there, story 54.23; the other
// groups' run folders below are not its).
func GrantFiles(profile, path string) error {
	sid, err := SID(profile)
	if err != nil {
		return err
	}
	return change(sid, path, &ourACE{mask: fileRead, flags: windows.OBJECT_INHERIT_ACE | windows.NO_PROPAGATE_INHERIT_ACE}, false)
}

// A grant or revoke changes the DACL of the object that was checked and nothing else
// (story 54.23). By name (SetNamedSecurityInfo) a folder swapped for a junction after the
// check would have the change land on the junction's target, so:
//
//   - the folder or file is opened by handle, never through a link at its end
//     (FILE_FLAG_OPEN_REPARSE_POINT), and refused when it is a reparse point itself or when
//     the filesystem names it (GetFinalPathNameByHandle) otherwise than the checked path (a
//     link above it, swapped in since); held without FILE_SHARE_DELETE, so it cannot be
//     renamed or removed while it is changed;
//   - its DACL is set on that handle with SetKernelObjectSecurity, which never walks the
//     tree by name;
//   - what the folder already holds gets what the new DACL passes down, by handles opened
//     relative to their folder's handle (NtCreateFile), never through a junction or
//     symbolic link (a name surrogate is skipped with all it points to), and only the
//     SID's inherited entries change there: each child's inherited entries of the SID are
//     recomputed from its parent's, as Windows' own propagation would; a child with a
//     protected DACL inherits nothing and is left alone.
//
// A grant changes what the folder holds first and the folder last (a walk that fails
// leaves the folder without the grant, so the next start walks again); a revoke changes
// the folder first (nothing new inherits the entry) and then what it holds.
func change(sid *windows.SID, path string, add *ourACE, revoking bool) error {
	h, dir, err := openChecked(path)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("appcontainer: %s: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil && !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
		return fmt.Errorf("appcontainer: %s: %w", path, err)
	}
	if add != nil && hasGrant(dacl, sid, windows.ACCESS_MASK(add.mask), add.flags) {
		return nil
	}
	if add == nil && dacl == nil {
		return nil // no DACL: no entry of the SID to take back
	}
	list, err := aces(dacl)
	if err != nil {
		return fmt.Errorf("appcontainer: %s: %w", path, err)
	}
	// the SID's explicit entries go; a new one is the last explicit entry (after any
	// explicit deny), before the inherited ones
	out := make([][]byte, 0, len(list)+1)
	placed := add == nil
	for _, a := range list {
		inherited := a[1]&windows.INHERITED_ACE != 0
		if !placed && inherited {
			out, placed = append(out, newACE(sid, add.mask, add.flags)), true
		}
		if !inherited && isOurs(a, sid) {
			continue
		}
		out = append(out, a)
	}
	if !placed {
		out = append(out, newACE(sid, add.mask, add.flags))
	}
	acl := buildACL(aclRevision(dacl), out)
	if revoking {
		if err := setDACL(h, sd, acl); err != nil {
			return fmt.Errorf("appcontainer: %s: %w", path, err)
		}
	}
	if dir {
		if err := propagate(h, path, oursIn(out, sid), sid, revoking); err != nil {
			return err
		}
	}
	if !revoking {
		if err := setDACL(h, sd, acl); err != nil {
			return fmt.Errorf("appcontainer: %s: %w", path, err)
		}
	}
	return nil
}

// ourACE is one allow entry of a profile's SID: its flags and mask.
type ourACE struct {
	mask  uint32
	flags uint8
}

// fileAttributeTagInfo is FILE_ATTRIBUTE_TAG_INFO.
type fileAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

func attributeTag(h windows.Handle) (fileAttributeTagInfo, error) {
	var info fileAttributeTagInfo
	err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return info, err
}

// finalPath is the path the filesystem names an open handle by (links above it resolved).
func finalPath(h windows.Handle) (string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0 /* FILE_NAME_NORMALIZED | VOLUME_NAME_DOS */)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return finalPathName(windows.UTF16ToString(buf[:n])), nil
		}
		buf = make([]uint16, n+1)
	}
}

// openChecked opens the checked object by handle and answers it only when it is that
// object: not a reparse point itself, and named by the filesystem as checked. The handle
// can read and set the DACL and list a folder; it does not share delete.
func openChecked(path string) (windows.Handle, bool, error) {
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, false, err
	}
	h, err := windows.CreateFile(p16,
		windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, false, fmt.Errorf("appcontainer: %s: %w", path, err)
	}
	fail := func(err error) (windows.Handle, bool, error) {
		_ = windows.CloseHandle(h)
		return 0, false, err
	}
	info, err := attributeTag(h)
	if err != nil {
		return fail(fmt.Errorf("appcontainer: %s: %w", path, err))
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fail(fmt.Errorf("appcontainer: %s is a link or another reparse point (tag 0x%08x): it changed after it was checked", path, info.ReparseTag))
	}
	final, err := finalPath(h)
	if err != nil {
		return fail(fmt.Errorf("appcontainer: %s: %w", path, err))
	}
	if pathKey(final) != pathKey(path) {
		return fail(fmt.Errorf("appcontainer: %s is %s on disk: it changed after it was checked", path, final))
	}
	return h, info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0, nil
}

// openChild opens one entry of an open folder by a handle relative to it: the name is one
// component, and a reparse point at it is opened itself, never followed.
func openChild(dir windows.Handle, name string, isDir bool) (windows.Handle, error) {
	u, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: dir, ObjectName: u}
	oa.Length = uint32(unsafe.Sizeof(oa))
	access := uint32(windows.READ_CONTROL | windows.WRITE_DAC | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	if isDir {
		access |= windows.FILE_LIST_DIRECTORY
	}
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, access, &oa, &iosb, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	return h, err
}

// fileFullDirInfo is FILE_FULL_DIR_INFO (EaSize holds the reparse tag of a reparse point).
type fileFullDirInfo struct {
	NextEntryOffset uint32
	FileIndex       uint32
	CreationTime    int64
	LastAccessTime  int64
	LastWriteTime   int64
	ChangeTime      int64
	EndOfFile       int64
	AllocationSize  int64
	FileAttributes  uint32
	FileNameLength  uint32
	EaSize          uint32
	FileName        [1]uint16
}

type dirEntry struct {
	name  string
	attrs uint32
	tag   uint32
}

// listDir lists an open folder by its handle.
func listDir(dir windows.Handle) ([]dirEntry, error) {
	words := make([]uint64, 8192) // 64 KiB, 8-byte aligned
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*8)
	class := uint32(windows.FileFullDirectoryRestartInfo)
	var out []dirEntry
	for {
		err := windows.GetFileInformationByHandleEx(dir, class, &buf[0], uint32(len(buf)))
		class = windows.FileFullDirectoryInfo
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		for off := 0; ; {
			info := (*fileFullDirInfo)(unsafe.Pointer(&buf[off]))
			name := unsafe.Slice(&info.FileName[0], info.FileNameLength/2)
			if n := windows.UTF16ToString(name); n != "." && n != ".." {
				out = append(out, dirEntry{name: n, attrs: info.FileAttributes, tag: info.EaSize})
			}
			if info.NextEntryOffset == 0 {
				break
			}
			off += int(info.NextEntryOffset)
		}
	}
}

// propagate gives what an open folder holds the entries of the SID its DACL passes down
// (parent: the SID's entries on the folder), never through a name surrogate (a junction or
// a symbolic link: skipped, and what it points to with it). A child that cannot be opened
// to change its DACL is skipped by a grant (nothing is given there) and fails a revoke.
func propagate(dir windows.Handle, dirPath string, parent []ourACE, sid *windows.SID, revoking bool) error {
	entries, err := listDir(dir)
	if err != nil {
		return fmt.Errorf("appcontainer: %s: %w", dirPath, err)
	}
	for _, e := range entries {
		if e.attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && nameSurrogate(e.tag) {
			continue
		}
		childPath := dirPath + `\` + e.name
		h, err := openChild(dir, e.name, e.attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0)
		switch {
		case errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND), errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND):
			continue // gone since it was listed
		case errors.Is(err, windows.STATUS_ACCESS_DENIED) && !revoking:
			continue
		case err != nil:
			return fmt.Errorf("appcontainer: %s: %w", childPath, err)
		}
		err = inheritInto(h, childPath, parent, sid, revoking)
		_ = windows.CloseHandle(h)
		if err != nil {
			return err
		}
	}
	return nil
}

func inheritInto(h windows.Handle, path string, parent []ourACE, sid *windows.SID, revoking bool) error {
	info, err := attributeTag(h)
	if err != nil {
		return fmt.Errorf("appcontainer: %s: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && nameSurrogate(info.ReparseTag) {
		return nil // made a link since it was listed
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("appcontainer: %s: %w", path, err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if ctrl&windows.SE_DACL_PROTECTED != 0 || dacl == nil || err != nil {
		return nil // inherits nothing
	}
	isDir := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	list, err := aces(dacl)
	if err != nil {
		return fmt.Errorf("appcontainer: %s: %w", path, err)
	}
	out := make([][]byte, 0, len(list)+len(parent))
	for _, a := range list {
		if a[1]&windows.INHERITED_ACE != 0 && isOurs(a, sid) {
			continue
		}
		out = append(out, a)
	}
	for _, p := range parent {
		if f, ok := inheritedFlags(p.flags, isDir); ok {
			out = append(out, newACE(sid, p.mask, f))
		}
	}
	if !sameACEs(list, out) {
		if err := setDACL(h, sd, buildACL(aclRevision(dacl), out)); err != nil {
			return fmt.Errorf("appcontainer: %s: %w", path, err)
		}
	}
	if isDir {
		return propagate(h, path, oursIn(out, sid), sid, revoking)
	}
	return nil
}

// aces copies a DACL's entries as they are.
func aces(dacl *windows.ACL) ([][]byte, error) {
	if dacl == nil {
		return nil, nil
	}
	out := make([][]byte, 0, dacl.AceCount)
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return nil, err
		}
		raw := unsafe.Slice((*byte)(unsafe.Pointer(ace)), ace.Header.AceSize)
		out = append(out, append([]byte(nil), raw...))
	}
	return out, nil
}

// isOurs: an allow entry of the SID.
func isOurs(a []byte, sid *windows.SID) bool {
	if len(a) < 8+8 || a[0] != windows.ACCESS_ALLOWED_ACE_TYPE {
		return false
	}
	return (*windows.SID)(unsafe.Pointer(&a[8])).Equals(sid)
}

// oursIn is the SID's allow entries in a DACL, explicit and inherited.
func oursIn(list [][]byte, sid *windows.SID) []ourACE {
	var out []ourACE
	for _, a := range list {
		if isOurs(a, sid) {
			out = append(out, ourACE{flags: a[1], mask: binary.LittleEndian.Uint32(a[4:8])})
		}
	}
	return out
}

func newACE(sid *windows.SID, mask uint32, flags uint8) []byte {
	n := int(windows.GetLengthSid(sid))
	a := make([]byte, 8+n)
	a[0], a[1] = windows.ACCESS_ALLOWED_ACE_TYPE, flags
	binary.LittleEndian.PutUint16(a[2:4], uint16(8+n))
	binary.LittleEndian.PutUint32(a[4:8], mask)
	copy(a[8:], unsafe.Slice((*byte)(unsafe.Pointer(sid)), n))
	return a
}

func sameACEs(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			return false
		}
	}
	return true
}

func aclRevision(dacl *windows.ACL) byte {
	if dacl == nil {
		return 2 // ACL_REVISION
	}
	return *(*byte)(unsafe.Pointer(dacl))
}

// buildACL lays entries out as an ACL (its header, then each entry), DWORD aligned.
func buildACL(revision byte, list [][]byte) *windows.ACL {
	size := 8
	for _, a := range list {
		size += len(a)
	}
	words := make([]uint32, (size+3)/4)
	b := unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*4)
	b[0] = revision
	binary.LittleEndian.PutUint16(b[2:4], uint16(size))
	binary.LittleEndian.PutUint16(b[4:6], uint16(len(list)))
	off := 8
	for _, a := range list {
		off += copy(b[off:], a)
	}
	return (*windows.ACL)(unsafe.Pointer(&words[0]))
}

// setDACL sets a DACL on an open handle, keeping the object's protected and
// auto-inherited flags; SetKernelObjectSecurity changes that object alone.
func setDACL(h windows.Handle, was *windows.SECURITY_DESCRIPTOR, acl *windows.ACL) error {
	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		return err
	}
	if err := sd.SetDACL(acl, true, false); err != nil {
		return err
	}
	ctrl, _, err := was.Control()
	if err != nil {
		return err
	}
	const keep = windows.SE_DACL_AUTO_INHERITED | windows.SE_DACL_PROTECTED
	if err := sd.SetControl(keep, ctrl&keep); err != nil {
		return err
	}
	err = windows.SetKernelObjectSecurity(h, windows.DACL_SECURITY_INFORMATION, sd)
	runtime.KeepAlive(acl)
	return err
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
