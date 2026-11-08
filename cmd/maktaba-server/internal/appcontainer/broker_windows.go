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

// The access a runner's duplicated handle holds: read, or write a subtitle's bytes (no
// FILE_WRITE_ATTRIBUTES, WRITE_DAC or DELETE: it cannot change the file but by its data).
const (
	brokerReadAccess  = windows.FILE_GENERIC_READ
	brokerWriteAccess = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
)

// fileBroker serves one runner's share folders (broker.go).
type fileBroker struct {
	roots   BrokerRoots
	mu      sync.Mutex
	pid     uint32
	process windows.Handle
}

func (b *fileBroker) started(pid uint32, process uintptr) {
	b.mu.Lock()
	b.pid, b.process = pid, windows.Handle(process)
	b.mu.Unlock()
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
	return env, b.started, func() { _ = l.Close() }, nil
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
	b.mu.Lock()
	pid, process := b.pid, b.process
	b.mu.Unlock()
	if pid == 0 || client != pid {
		return
	}
	in := bufio.NewScanner(c)
	in.Buffer(make([]byte, 64<<10), 64<<10)
	out := json.NewEncoder(c)
	for in.Scan() {
		var req BrokerRequest
		var reply BrokerReply
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			reply = refusal(fmt.Errorf("not a request: %w", errRefused))
		} else {
			reply = b.do(req, process)
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
		root, p, err := b.roots.Confine(req.Path, req.Write)
		if err != nil {
			return refusal(err)
		}
		h, err := b.open(root, p, req.Write)
		if err != nil {
			return refusal(err)
		}
		defer func() { _ = windows.CloseHandle(h) }()
		access := uint32(brokerReadAccess)
		if req.Write {
			access = brokerWriteAccess
		}
		var theirs windows.Handle
		if err := windows.DuplicateHandle(windows.CurrentProcess(), h, process, &theirs, access, false, 0); err != nil {
			return refusal(err)
		}
		return BrokerReply{Handle: uint64(theirs)}
	case "stat", "list":
		root, p, err := b.roots.Confine(req.Path, false)
		if err != nil {
			return refusal(err)
		}
		h, err := openConfined(root.Path, p, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, 0)
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
		if !strings.EqualFold(src[:strings.LastIndex(src, `\`)], dst[:strings.LastIndex(dst, `\`)]) {
			return refusal(fmt.Errorf("%s: a subtitle is renamed in its own folder only: %w", dst, errRefused))
		}
		h, err := openConfined(root.Path, src, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, 0)
		if err != nil {
			return refusal(err)
		}
		_ = windows.CloseHandle(h)
		if h, err := openConfined(root.Path, dst, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, 0); err == nil {
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
		h, err := openConfined(root.Path, p, windows.DELETE|windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, windows.FILE_FLAG_DELETE_ON_CLOSE)
		if err != nil {
			return refusal(err)
		}
		_ = windows.CloseHandle(h) // removed as it closes: the object that was checked
		return BrokerReply{}
	}
	return refusal(fmt.Errorf("%s: not an operation: %w", req.Op, errRefused))
}

// open opens a brokered file for the runner: an existing file to read, or a subtitle to
// write (made or emptied; one that exists must not be a link).
func (b *fileBroker) open(root BrokerRoot, p string, write bool) (windows.Handle, error) {
	if !write {
		h, err := openConfined(root.Path, p, windows.FILE_GENERIC_READ, windows.OPEN_EXISTING, 0)
		if err != nil {
			return 0, err
		}
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			_ = windows.CloseHandle(h)
			return 0, fmt.Errorf("%s: not a file: %w", p, errRefused)
		}
		return h, nil
	}
	if h, err := openConfined(root.Path, p, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, 0); err == nil {
		_ = windows.CloseHandle(h)
	} else if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return 0, err
	}
	return openConfined(root.Path, p, windows.GENERIC_WRITE|windows.FILE_READ_ATTRIBUTES, windows.CREATE_ALWAYS, 0)
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
func openConfined(root, p string, access uint32, disposition uint32, flags uint32) (windows.Handle, error) {
	root = strings.TrimRight(root, `\`)
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
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT|flags, 0)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", p, err)
	}
	if link, err := brokerIsReparse(h); err != nil || link {
		_ = windows.CloseHandle(h)
		return 0, fmt.Errorf("%s: a link or another reparse point: %w", p, errRefused)
	}
	final, err := brokerFinalPath(h)
	if err != nil || !strings.EqualFold(final, p) {
		_ = windows.CloseHandle(h)
		return 0, fmt.Errorf("%s is named %q by the filesystem: %w", p, final, errRefused)
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
