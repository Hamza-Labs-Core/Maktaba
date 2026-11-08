//go:build windows

package appcontainer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pluginrelay"
)

// brokerReadAccess is what a runner's duplicated handle holds: read, never write, delete,
// attributes or the DACL.
const brokerReadAccess = windows.FILE_GENERIC_READ

// fileBroker serves one runner's share folders (broker.go). It keeps its own handle of the
// runner's process (duplicated when the runner starts, closed when the broker stops, with no
// request in flight).
type fileBroker struct {
	roots   BrokerRoots
	mu      sync.RWMutex
	pid     uint32
	process windows.Handle
	stopped bool
}

func (b *fileBroker) started(pid uint32, process uintptr) {
	var own windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(process), windows.CurrentProcess(), &own,
		windows.PROCESS_DUP_HANDLE, false, 0); err != nil {
		return // not served: every request is refused
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		_ = windows.CloseHandle(own)
		return
	}
	b.pid, b.process = pid, own
}

func (b *fileBroker) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	if b.process != 0 {
		_ = windows.CloseHandle(b.process)
		b.process = 0
	}
}

// startBroker serves a runner's share folders on a pipe only its AppContainer may open,
// and answers what the runner's environment needs, the hook that learns the runner's
// process, and the stop.
func startBroker(profile string, roots BrokerRoots) ([]string, func(uint32, uintptr), func(), error) {
	sid, err := SIDString(profile)
	if err != nil {
		return nil, nil, nil, err
	}
	name, err := pluginrelay.PipeName()
	if err != nil {
		return nil, nil, nil, err
	}
	name += "-files"
	l, err := pluginrelay.ListenFor(name, sid)
	if err != nil {
		return nil, nil, nil, err
	}
	b := &fileBroker{roots: roots}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go b.serve(c)
		}
	}()
	doc, _ := json.Marshal(roots)
	env := []string{EnvFilesPipe + "=" + name, EnvFilesRoots + "=" + string(doc)}
	return env, b.started, func() { _ = l.Close(); b.stop() }, nil
}

// serve answers one connection's requests, one line each, when it is the runner's own
// process (not one of its tools, not another process of the AppContainer).
func (b *fileBroker) serve(c io.ReadWriteCloser) {
	defer func() { _ = c.Close() }()
	f, ok := c.(*os.File)
	if !ok {
		return
	}
	var client uint32
	if err := windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &client); err != nil {
		return
	}
	b.mu.RLock()
	pid := b.pid
	b.mu.RUnlock()
	if pid == 0 || client != pid {
		// said, so a runner the broker never serves is seen in the log, not only in its refusals
		fmt.Fprintf(os.Stderr, `{"event":"plugin_runner_broker_client_refused","client":%d,"runner":%d}`+"\n", client, pid)
		return
	}
	in := bufio.NewScanner(c)
	in.Buffer(make([]byte, 64<<10), MaxBrokeredWrite*2)
	out := json.NewEncoder(c)
	for in.Scan() {
		var req BrokerRequest
		var reply BrokerReply
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			reply = refusal(fmt.Errorf("not a request: %w", errRefused))
		} else {
			b.mu.RLock()
			if b.process == 0 {
				reply = refusal(fmt.Errorf("the runner has exited: %w", errRefused))
			} else {
				reply = b.do(req, b.process)
			}
			b.mu.RUnlock()
		}
		if err := out.Encode(reply); err != nil {
			return
		}
	}
}

func refusal(err error) BrokerReply {
	errno := 13
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		errno = 2
	}
	return BrokerReply{Error: err.Error(), Errno: errno}
}

func (b *fileBroker) do(req BrokerRequest, process windows.Handle) BrokerReply {
	switch req.Op {
	case "open":
		root, p, err := b.roots.Confine(req.Path, false)
		if err != nil {
			return refusal(err)
		}
		h, err := openConfined(root.Path, p, windows.FILE_GENERIC_READ, windows.OPEN_EXISTING, nil)
		if err != nil {
			return refusal(err)
		}
		defer func() { _ = windows.CloseHandle(h) }()
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			return refusal(fmt.Errorf("%s: not a file: %w", p, errRefused))
		}
		var theirs windows.Handle
		if err := windows.DuplicateHandle(windows.CurrentProcess(), h, process, &theirs, brokerReadAccess, false, 0); err != nil {
			return refusal(err)
		}
		return BrokerReply{Handle: uint64(theirs)}
	case "write":
		root, p, err := b.roots.Confine(req.Path, true)
		if err != nil {
			return refusal(err)
		}
		if len(req.Data) > MaxBrokeredWrite {
			return refusal(fmt.Errorf("%s: a subtitle over %d bytes: %w", p, MaxBrokeredWrite, errRefused))
		}
		if err := writeConfined(root.Path, p, req.Data); err != nil {
			return refusal(err)
		}
		return BrokerReply{}
	case "stat", "list":
		root, p, err := b.roots.Confine(req.Path, false)
		if err != nil {
			return refusal(err)
		}
		h, err := openConfined(root.Path, p, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, nil)
		if err != nil {
			return refusal(err)
		}
		var info windows.ByHandleFileInformation
		err = windows.GetFileInformationByHandle(h, &info)
		_ = windows.CloseHandle(h)
		if err != nil {
			return refusal(err)
		}
		dir := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
		if req.Op == "stat" {
			return BrokerReply{Size: int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow), MtimeNs: info.LastWriteTime.Nanoseconds(), Dir: dir}
		}
		if !dir {
			return refusal(fmt.Errorf("%s: not a folder: %w", p, errRefused))
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return refusal(err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return BrokerReply{Names: names}
	case "replace":
		root, src, err := b.roots.Confine(req.Path, true)
		if err != nil {
			return refusal(err)
		}
		_, dst, err := b.roots.Confine(req.To, true)
		if err != nil {
			return refusal(err)
		}
		if asciiFold(src[:strings.LastIndex(src, `\`)]) != asciiFold(dst[:strings.LastIndex(dst, `\`)]) {
			return refusal(fmt.Errorf("%s: a subtitle is renamed in its own folder only: %w", dst, errRefused))
		}
		h, err := openConfined(root.Path, src, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, nil)
		if err != nil {
			return refusal(err)
		}
		_ = windows.CloseHandle(h)
		if h, err := openConfined(root.Path, dst, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, nil); err == nil {
			_ = windows.CloseHandle(h)
		} else if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return refusal(err)
		}
		from, _ := windows.UTF16PtrFromString(src)
		to, _ := windows.UTF16PtrFromString(dst)
		if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING); err != nil {
			return refusal(err)
		}
		return BrokerReply{}
	case "remove":
		root, p, err := b.roots.Confine(req.Path, true)
		if err != nil {
			return refusal(err)
		}
		h, err := openConfined(root.Path, p, windows.DELETE|windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, nil)
		if err != nil {
			return refusal(err)
		}
		// removed only now, on the handle that was checked
		err = deleteByHandle(h)
		_ = windows.CloseHandle(h)
		if err != nil {
			return refusal(err)
		}
		return BrokerReply{}
	}
	return refusal(fmt.Errorf("%s: not an operation: %w", req.Op, errRefused))
}

// writeConfined writes a subtitle's whole file: one that exists must not be a link; it is
// opened (or made) and checked before a byte of it changes, emptied and written only then,
// and one made here that fails the check is removed again.
func writeConfined(root, p string, data []byte) error {
	existed := true
	if h, err := openConfined(root, p, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, nil); err == nil {
		_ = windows.CloseHandle(h)
	} else if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		existed = false
	} else {
		return err
	}
	h, err := openConfined(root, p, windows.GENERIC_WRITE|windows.DELETE|windows.FILE_READ_ATTRIBUTES, windows.OPEN_ALWAYS,
		func(h windows.Handle) {
			if !existed {
				_ = deleteByHandle(h)
			}
		})
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.SetEndOfFile(h); err != nil { // the new handle is at offset 0
		return err
	}
	for len(data) > 0 {
		var n uint32
		if err := windows.WriteFile(h, data, &n, nil); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

// fileDispositionInfo is FILE_DISPOSITION_INFO.
type fileDispositionInfo struct{ DeleteFile bool }

func deleteByHandle(h windows.Handle) error {
	info := fileDispositionInfo{DeleteFile: true}
	return windows.SetFileInformationByHandle(h, windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
}

// brokerAttrTag is FILE_ATTRIBUTE_TAG_INFO.
type brokerAttrTag struct {
	FileAttributes uint32
	ReparseTag     uint32
}

func brokerIsReparse(h windows.Handle) (bool, error) {
	var info brokerAttrTag
	err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, err
}

// openConfined opens p, a path Confine answered under root: the root and every folder
// down to p's own are opened in turn without following a link and must not be one (no
// junction or symbolic link on the share leads out of the root; the runner can make none,
// it writes subtitle files by their data only), then p itself the same way, which must not
// be one either and which the filesystem must name as asked (any case: an 8.3 alias or
// another spelling is refused).
func openConfined(root, p string, access uint32, disposition uint32, refused func(windows.Handle)) (windows.Handle, error) {
	root = strings.TrimRight(root, `\`)
	// the request starts with the root's own bytes but for ASCII case (Confine compared so)
	if !underRoot(p, root) {
		return 0, fmt.Errorf("%s: not under %s: %w", p, root, errRefused)
	}
	rel := strings.TrimPrefix(p[len(root):], `\`)
	cur := root
	parts := []string{}
	if rel != "" {
		parts = strings.Split(rel, `\`)
	}
	for i := 0; i <= len(parts)-1; i++ {
		if err := notALink(cur); err != nil {
			return 0, err
		}
		cur += `\` + parts[i]
	}
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(p16, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, disposition,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", p, err)
	}
	refuse := func(err error) (windows.Handle, error) {
		if refused != nil {
			refused(h)
		}
		_ = windows.CloseHandle(h)
		return 0, err
	}
	if link, err := brokerIsReparse(h); err != nil || link {
		return refuse(fmt.Errorf("%s: a link or another reparse point: %w", p, errRefused))
	}
	final, err := brokerFinalPath(h)
	if err != nil || asciiFold(strings.TrimRight(final, `\`)) != asciiFold(strings.TrimRight(p, `\`)) {
		return refuse(fmt.Errorf("%s is named %q by the filesystem: %w", p, final, errRefused))
	}
	return h, nil
}

func notALink(dir string) error {
	d16, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(d16, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if link, err := brokerIsReparse(h); err != nil || link {
		return fmt.Errorf("%s: a link or another reparse point: %w", dir, errRefused)
	}
	return nil
}

// brokerFinalPath is the filesystem's own name of an open share file, as \\server\share\…
func brokerFinalPath(h windows.Handle) (string, error) {
	buf := make([]uint16, 1024)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0 /* FILE_NAME_NORMALIZED | VOLUME_NAME_DOS */)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			s := windows.UTF16ToString(buf[:n])
			if rest, ok := strings.CutPrefix(s, `\\?\UNC\`); ok {
				return `\\` + rest, nil
			}
			return strings.TrimPrefix(s, `\\?\`), nil
		}
		buf = make([]uint16, n+1)
	}
}

var (
	netapi32            = windows.NewLazySystemDLL("netapi32.dll")
	procNetShareGetInfo = netapi32.NewProc("NetShareGetInfo")
)

// shareInfo2 is SHARE_INFO_2.
type shareInfo2 struct {
	Netname     *uint16
	Type        uint32
	Remark      *uint16
	Permissions uint32
	MaxUses     uint32
	CurrentUses uint32
	Path        *uint16
	Passwd      *uint16
}

// localHost reports a host name that is this machine.
func localHost(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" || h == "." || h == "::1" || strings.HasPrefix(h, "127.") {
		return true
	}
	if name, err := os.Hostname(); err == nil {
		n := strings.ToLower(name)
		if h == n || h == strings.SplitN(n, ".", 2)[0] || strings.SplitN(h, ".", 2)[0] == n {
			return true
		}
	}
	return false
}

// shareLocalPath maps \\host\share\rest on a share this machine serves to its folder.
func shareLocalPath(root string) (string, bool, error) {
	parts := strings.SplitN(strings.TrimLeft(strings.ReplaceAll(root, "/", `\`), `\`), `\`, 3)
	if len(parts) < 2 || !localHost(parts[0]) {
		return "", false, nil
	}
	name, err := windows.UTF16PtrFromString(parts[1])
	if err != nil {
		return "", false, err
	}
	var buf *byte
	if r, _, _ := procNetShareGetInfo.Call(0, uintptr(unsafe.Pointer(name)), 2, uintptr(unsafe.Pointer(&buf))); r != 0 {
		return "", false, fmt.Errorf("this machine's share %s: NetShareGetInfo %d", parts[1], r)
	}
	defer func() { _ = windows.NetApiBufferFree(buf) }()
	info := (*shareInfo2)(unsafe.Pointer(buf))
	local := windows.UTF16PtrToString(info.Path)
	if len(parts) == 3 {
		local = strings.TrimRight(local, `\`) + `\` + parts[2]
	}
	return local, true, nil
}
