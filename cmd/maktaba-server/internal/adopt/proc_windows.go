//go:build windows

package adopt

import (
	"encoding/binary"
	"path/filepath"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processCwd is a process's current directory, read from its process
// environment block (one of this user's processes; "" when it cannot be read).
// Another process's addresses are read as numbers, never as Go pointers.
func processCwd(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(h) }()
	// the call wants an aligned buffer: words, not bytes (and numbers, not the
	// struct, whose PEB pointer is another process's)
	var pbi [unsafe.Sizeof(windows.PROCESS_BASIC_INFORMATION{}) / unsafe.Sizeof(uintptr(0))]uintptr
	if windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation, unsafe.Pointer(&pbi[0]), uint32(unsafe.Sizeof(pbi)), nil) != nil {
		return ""
	}
	peb := pbi[unsafe.Offsetof(windows.PROCESS_BASIC_INFORMATION{}.PebBaseAddress)/unsafe.Sizeof(uintptr(0))]
	params, ok := readWord(h, peb+unsafe.Offsetof(windows.PEB{}.ProcessParameters))
	if !ok || params == 0 {
		return ""
	}
	dos := params + unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.CurrentDirectory) + unsafe.Offsetof(windows.CURDIR{}.DosPath)
	us := make([]byte, unsafe.Sizeof(windows.NTUnicodeString{}))
	if !readMem(h, dos, us) {
		return ""
	}
	n := binary.LittleEndian.Uint16(us[unsafe.Offsetof(windows.NTUnicodeString{}.Length):])
	buf := word(us, unsafe.Offsetof(windows.NTUnicodeString{}.Buffer))
	if n == 0 || n%2 != 0 || buf == 0 {
		return ""
	}
	raw := make([]byte, n)
	if !readMem(h, buf, raw) {
		return ""
	}
	units := make([]uint16, n/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	return filepath.Clean(string(utf16.Decode(units)))
}

// word is the pointer-sized number at off in b.
func word(b []byte, off uintptr) uintptr {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return uintptr(binary.LittleEndian.Uint64(b[off:]))
	}
	return uintptr(binary.LittleEndian.Uint32(b[off:]))
}

func readWord(h windows.Handle, addr uintptr) (uintptr, bool) {
	b := make([]byte, unsafe.Sizeof(uintptr(0)))
	if !readMem(h, addr, b) {
		return 0, false
	}
	return word(b, 0), true
}

func readMem(h windows.Handle, addr uintptr, into []byte) bool {
	var n uintptr
	return windows.ReadProcessMemory(h, addr, &into[0], uintptr(len(into)), &n) == nil && n == uintptr(len(into))
}
