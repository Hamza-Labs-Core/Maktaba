package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/mediaaccess"
)

// The container's preflight (Epic 59 story 59.01): what `maktaba-server container` checks
// before anything else, so a NAS shows the container as stopped with a reason rather than a
// restart loop. In order:
//
//  1. uid 0 is refused: the image has no root step (no PUID/PGID, no chown); the container's
//     user is set by compose's `user:`, TrueNAS's Run As or Unraid's --user.
//  2. a privileged container, or one with the Docker socket, is refused: privileged is a
//     bounding set (CapBnd) holding CAP_SYS_ADMIN with /sys mounted read-write (CapEff
//     cannot tell: a non-root process has none even in a privileged container); the socket
//     is one at a known path or any mount whose target ends in docker.sock.
//  3. the volume must take a real write (<volume>/.write-test made and removed), not a mode
//     comparison: a group-writable folder (TrueNAS's Apps preset) works.
//  4. a uid /etc/passwd lacks gets a name: a passwd and a group file under <volume>/.nss and
//     libnss_wrapper preloaded for every child (initdb, postgres and Python's pwd look the
//     user up), as the official Postgres image does.
//
// A refusal exits 78 (EX_CONFIG) with its message in English, then Arabic on the next line.

// exitConfig is the preflight's exit on a refusal (sysexits' EX_CONFIG): the restart
// policy of a NAS stops on it.
const exitConfig = 78

// capSysAdmin is CAP_SYS_ADMIN's bit in a capability set.
const capSysAdmin = 21

// dockerSockets are the Docker socket's known paths inside a container.
var dockerSockets = []string{"/var/run/docker.sock", "/run/docker.sock"}

// nssName is the name the preflight gives a uid /etc/passwd lacks.
const nssName = "maktaba"

// containerTexts are the preflight's messages, key → {en, ar}.
var containerTexts = map[string][2]string{
	"container.write_denied": {
		"Maktaba cannot write to its data folder {path} as user {uid} (group {gid}). Give this user Read/Write on the folder you mounted there, then start Maktaba again.",
		"لا يستطيع Maktaba الكتابة في مجلد بياناته {path} بصفته المستخدم {uid} (المجموعة {gid}). امنح هذا المستخدم صلاحية القراءة والكتابة على المجلد الذي ربطته هناك، ثم شغّل Maktaba من جديد.",
	},
	"container.write_owner": {
		"The folder belongs to user {owner}: give user {uid} access, or run the container as {owner} again.",
		"المجلد ملك المستخدم {owner}: امنح المستخدم {uid} صلاحية الوصول، أو شغّل الحاوية بصفة المستخدم {owner} كما كانت.",
	},
	// story 59.10: on TrueNAS the data folder is a dataset, fixed in its permissions editor
	"container.write_truenas": {
		"Edit the dataset's permissions and add an ACL entry for user {uid} with Modify, or use the Apps preset.",
		"عدّل أذونات مجموعة البيانات وأضف إدخال ACL للمستخدم {uid} بصلاحية التعديل، أو استخدم الإعداد المسبق Apps.",
	},
	"container.root_refused": {
		"Maktaba does not run as root. Set the container's user to a normal user (for example 1000:1000).",
		"لا يعمل Maktaba بصلاحيات الجذر. اضبط مستخدم الحاوية على مستخدم عادي (مثل 1000:1000).",
	},
	"container.privileged_refused": {
		"Maktaba does not run in a privileged container or with the Docker socket. Remove that setting and start it again.",
		"لا يعمل Maktaba في حاوية ذات امتيازات كاملة ولا مع مقبس Docker. أزل هذا الإعداد وشغّله من جديد.",
	},
}

// containerText is key's English and Arabic lines, {name}s replaced from kv pairs.
func containerText(key string, kv ...string) string {
	t := containerTexts[key]
	r := strings.NewReplacer(kv...)
	return r.Replace(t[0]) + "\n" + r.Replace(t[1])
}

// preflight is what the container's start checks, with every path it reads replaceable
// (the tests give fake /proc files and a temporary volume).
type preflight struct {
	uid, gid  int
	volume    string   // the data volume (/var/lib/maktaba, the image's HOME)
	status    string   // /proc/self/status
	mountinfo string   // /proc/self/mountinfo
	sockets   []string // dockerSockets
	passwd    string   // /etc/passwd
	group     string   // /etc/group
	// nssLib is libnss_wrapper.so ("" when the image has none: no name is given).
	nssLib string
	// platform is MAKTABA_PLATFORM ("" none): a refusal says the fix in that NAS's words.
	platform string
}

func newPreflight() preflight {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/var/lib/maktaba"
	}
	return preflight{
		uid: os.Getuid(), gid: os.Getgid(), volume: home,
		status: "/proc/self/status", mountinfo: "/proc/self/mountinfo", sockets: dockerSockets,
		passwd: "/etc/passwd", group: "/etc/group", nssLib: findNSSWrapper(),
		platform: mediaaccess.PlatformFromEnv(),
	}
}

// findNSSWrapper is Debian's libnss_wrapper.so (libnss-wrapper: /usr/lib/<multiarch>/), ""
// when there is none.
func findNSSWrapper() string {
	for _, pat := range []string{"/usr/lib/*/libnss_wrapper.so", "/usr/lib/libnss_wrapper.so", "/usr/lib64/libnss_wrapper.so"} {
		if m, _ := filepath.Glob(pat); len(m) > 0 {
			return m[0]
		}
	}
	return ""
}

// refusal is a preflight's stop: its message (English, then Arabic).
type refusal struct{ message string }

func (r *refusal) Error() string { return r.message }

// run checks in the order above and returns the variables every child gets (the
// nss_wrapper ones; none when the uid has a name), or the refusal.
func (p preflight) run() ([]string, error) {
	if p.uid == 0 {
		return nil, &refusal{containerText("container.root_refused")}
	}
	if why := p.privileged(); why != "" {
		return nil, &refusal{containerText("container.privileged_refused") + "\n(" + why + ")"}
	}
	if err := p.writeCheck(); err != nil {
		return nil, err
	}
	return p.nssEnv()
}

// privileged says why the container counts as privileged or holding the Docker socket ("":
// it does not).
func (p preflight) privileged() string {
	if bnd, ok := capBounding(p.status); ok && bnd&(1<<capSysAdmin) != 0 && sysWritable(p.mountinfo) {
		return "CapBnd holds CAP_SYS_ADMIN and /sys is mounted read-write: --privileged"
	}
	for _, s := range p.sockets {
		if fi, err := os.Stat(s); err == nil && fi.Mode()&fs.ModeSocket != 0 {
			return s + " is mounted"
		}
	}
	for _, m := range mounts(p.mountinfo) {
		if strings.HasSuffix(m.target, "docker.sock") {
			return m.target + " is mounted"
		}
	}
	return ""
}

// capBounding is the CapBnd line of a /proc/<pid>/status.
func capBounding(status string) (uint64, bool) {
	b, err := os.ReadFile(status)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "CapBnd:"); ok {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// mount is one line of a mountinfo: its target and its per-mount options.
type mount struct{ target, opts string }

// mounts reads a /proc/<pid>/mountinfo (fields: id parent major:minor root target options
// ...); targets keep their octal escapes (\040 a space).
func mounts(path string) []mount {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []mount
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 6 {
			continue
		}
		out = append(out, mount{target: fields[4], opts: fields[5]})
	}
	return out
}

// sysWritable: /sys is mounted read-write (Docker mounts it read-only but in a privileged
// container).
func sysWritable(mountinfo string) bool {
	for _, m := range mounts(mountinfo) {
		if m.target == "/sys" {
			for _, o := range strings.Split(m.opts, ",") {
				if o == "rw" {
					return true
				}
			}
			return false
		}
	}
	return false
}

// writeTest is the file the write check makes and removes in the volume.
const writeTest = ".write-test"

// writeCheck makes and removes <volume>/.write-test: a real write as this uid.
func (p preflight) writeCheck() error {
	path := filepath.Join(p.volume, writeTest)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		_, err = f.WriteString("maktaba\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if rerr := os.Remove(path); err == nil {
			err = rerr
		}
	}
	if err == nil {
		return nil
	}
	uid, gid := strconv.Itoa(p.uid), strconv.Itoa(p.gid)
	msg := containerText("container.write_denied", "{path}", p.volume, "{uid}", uid, "{gid}", gid)
	if owner, ok := ownerOf(p.volume); ok && owner != p.uid && owner != 0 { // never "run as root"
		msg += "\n" + containerText("container.write_owner", "{owner}", strconv.Itoa(owner), "{uid}", uid)
	}
	if strings.EqualFold(strings.TrimSpace(p.platform), mediaaccess.PlatformTrueNAS) {
		msg += "\n" + containerText("container.write_truenas", "{uid}", uid)
	}
	return &refusal{msg + "\n(" + err.Error() + ")"}
}

// nssDir is where the passwd and group files of a uid /etc/passwd lacks go in the volume.
const nssDir = ".nss"

// nssEnv gives the uid a name when /etc/passwd has none: <volume>/.nss/passwd and group (the
// image's own, the uid's line added) and libnss_wrapper preloaded. Nothing when the uid
// has a name (the image's 1000) or the image has no libnss_wrapper.
func (p preflight) nssEnv() ([]string, error) {
	pw, err := os.ReadFile(p.passwd)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if hasID(string(pw), p.uid) || p.nssLib == "" {
		return nil, nil
	}
	gr, err := os.ReadFile(p.group)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	dir := filepath.Join(p.volume, nssDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	passwd := withoutName(string(pw), nssName) +
		fmt.Sprintf("%s:x:%d:%d:Maktaba:%s:/usr/sbin/nologin\n", nssName, p.uid, p.gid, p.volume)
	group := string(gr)
	if !strings.HasSuffix(group, "\n") && group != "" {
		group += "\n"
	}
	if !hasID(group, p.gid) {
		group = withoutName(group, nssName) + fmt.Sprintf("%s:x:%d:\n", nssName, p.gid)
	}
	pf, gf := filepath.Join(dir, "passwd"), filepath.Join(dir, "group")
	if err := os.WriteFile(pf, []byte(passwd), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(gf, []byte(group), 0o644); err != nil {
		return nil, err
	}
	return []string{"LD_PRELOAD=" + p.nssLib, "NSS_WRAPPER_PASSWD=" + pf, "NSS_WRAPPER_GROUP=" + gf}, nil
}

// hasID: a passwd or group file has an entry whose third field (the uid or gid) is id.
func hasID(file string, id int) bool {
	want := strconv.Itoa(id)
	for _, line := range strings.Split(file, "\n") {
		if f := strings.Split(line, ":"); len(f) >= 3 && f[2] == want {
			return true
		}
	}
	return false
}

// withoutName is a passwd or group file without name's entries (the image's own maktaba,
// 1000, would shadow the uid's), ending with a newline.
func withoutName(file, name string) string {
	var b strings.Builder
	for _, line := range strings.Split(file, "\n") {
		if line == "" || strings.HasPrefix(line, name+":") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// withEnv is env with kvs set (each replacing the same name's earlier value).
func withEnv(env []string, kvs ...string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		keep := true
		for _, set := range kvs {
			if sk, _, _ := strings.Cut(set, "="); sk == k {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return append(out, kvs...)
}
