//go:build !windows

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Docker's default bounding set (no CAP_SYS_ADMIN) and a privileged container's (all).
const (
	capBndDefault    = "00000000a80425fb"
	capBndPrivileged = "000001ffffffffff"
)

// mountinfo lines: /sys read-only (Docker's default) or read-write (--privileged).
const (
	sysRO = "600 590 0:30 / /sys ro,nosuid,nodev,noexec,relatime - sysfs sysfs ro\n"
	sysRW = "600 590 0:30 / /sys rw,nosuid,nodev,noexec,relatime - sysfs sysfs rw\n"
)

const etcPasswd = "root:x:0:0:root:/root:/bin/bash\nmaktaba:x:1000:1000::/var/lib/maktaba:/usr/sbin/nologin\n"
const etcGroup = "root:x:0:\nusers:x:100:\nmaktaba:x:1000:\n"

// preflightFor is a preflight on fake /proc and /etc files and a temporary volume, as uid
// 1000 with a libnss_wrapper.
func preflightFor(t *testing.T, capBnd, mountinfo string) preflight {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	vol := filepath.Join(dir, "volume")
	if err := os.Mkdir(vol, 0o755); err != nil {
		t.Fatal(err)
	}
	return preflight{
		uid: 1000, gid: 1000, volume: vol,
		status:    write("status", "Name:\tmaktaba-server\nCapEff:\t0000000000000000\nCapBnd:\t"+capBnd+"\n"),
		mountinfo: write("mountinfo", "590 1 0:29 / / rw,relatime - overlay overlay rw\n"+mountinfo),
		sockets:   []string{filepath.Join(dir, "docker.sock")},
		passwd:    write("passwd", etcPasswd), group: write("group", etcGroup),
		nssLib: "/usr/lib/x86_64-linux-gnu/libnss_wrapper.so",
	}
}

func refusalOf(t *testing.T, err error) string {
	t.Helper()
	var r *refusal
	if !errors.As(err, &r) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if lines := strings.Split(r.message, "\n"); len(lines) < 2 || !strings.ContainsAny(lines[1], "ابتثجحخدذرزسشصضطظعغفقكلمنهوي") {
		t.Fatalf("the refusal must have its Arabic on the second line: %q", r.message)
	}
	return r.message
}

// T08: a full bounding set (with /sys read-write) is a privileged container, even as uid
// 1000 whose effective set is empty.
func TestPreflightRefusesPrivileged(t *testing.T) {
	_, err := preflightFor(t, capBndPrivileged, sysRW).run()
	if msg := refusalOf(t, err); !strings.Contains(msg, "privileged container") || !strings.Contains(msg, "CAP_SYS_ADMIN") {
		t.Fatalf("message: %q", msg)
	}
}

// T09: Docker's default bounding set is allowed; so is an added CAP_SYS_ADMIN with /sys
// read-only (--cap-add: reported in Settings, not refused).
func TestPreflightAllowsDefaultCaps(t *testing.T) {
	for _, c := range []struct{ bnd, mi string }{{capBndDefault, sysRO}, {capBndDefault, sysRW}, {capBndPrivileged, sysRO}} {
		if env, err := preflightFor(t, c.bnd, c.mi).run(); err != nil || env != nil {
			t.Fatalf("CapBnd %s: %v %v", c.bnd, env, err)
		}
	}
}

func TestPreflightRefusesRoot(t *testing.T) {
	p := preflightFor(t, capBndDefault, sysRO)
	p.uid, p.gid = 0, 0
	if msg := refusalOf(t, func() error { _, err := p.run(); return err }()); !strings.Contains(msg, "does not run as root") ||
		!strings.Contains(msg, "1000:1000") {
		t.Fatalf("message: %q", msg)
	}
}

// T05: the Docker socket, at a known path or mounted anywhere else.
func TestPreflightRefusesTheDockerSocket(t *testing.T) {
	p := preflightFor(t, capBndDefault, sysRO)
	ln, err := net.Listen("unix", p.sockets[0])
	if err != nil {
		t.Skipf("a unix socket here: %v", err)
	}
	defer ln.Close()
	_, err = p.run()
	if msg := refusalOf(t, err); !strings.Contains(msg, "Docker socket") || !strings.Contains(msg, p.sockets[0]) {
		t.Fatalf("message: %q", msg)
	}

	p = preflightFor(t, capBndDefault, sysRO+"700 590 0:5 /docker.sock /srv/my\\040docker.sock rw - tmpfs tmpfs rw\n")
	if _, err := p.run(); !strings.Contains(refusalOf(t, err), "/srv/my\\040docker.sock") {
		t.Fatal("a docker.sock mounted elsewhere is refused")
	}
}

// T02: a volume the uid cannot write stops with the path, the uid, the gid and the owner.
func TestPreflightWriteCheck(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	p := preflightFor(t, capBndDefault, sysRO)
	p.uid, p.gid = os.Getuid(), 568
	if err := os.Chmod(p.volume, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p.volume, 0o755) })
	_, err := p.run()
	msg := refusalOf(t, err)
	for _, want := range []string{p.volume, "as user " + strconv.Itoa(p.uid), "(group 568)", "Read/Write", "بصفته المستخدم"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q: %q", want, msg)
		}
	}
	if strings.Contains(msg, "belongs to user") {
		t.Fatalf("the owner is the uid itself: no owner line: %q", msg)
	}
	if strings.Contains(msg, "ACL") {
		t.Fatalf("no platform: no TrueNAS words: %q", msg)
	}

	// story 59.10: on TrueNAS the fix names the dataset's ACL and the Apps preset
	p.platform = "truenas"
	_, err = p.run()
	msg = refusalOf(t, err)
	for _, want := range []string{"Edit the dataset's permissions and add an ACL entry for user " + strconv.Itoa(p.uid) + " with Modify, or use the Apps preset.", "الإعداد المسبق Apps"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("TrueNAS message lacks %q: %q", want, msg)
		}
	}
	p.platform = ""

	// another uid's folder (a 1000-owned volume started as 568): the owner is named
	p.uid = os.Getuid() + 1
	_, err = p.run()
	if msg := refusalOf(t, err); !strings.Contains(msg, "belongs to user "+strconv.Itoa(os.Getuid())) {
		t.Fatalf("message: %q", msg)
	}

	// writable again: the check leaves nothing behind
	_ = os.Chmod(p.volume, 0o755)
	p.uid = os.Getuid()
	if _, err := p.run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.volume, writeTest)); !os.IsNotExist(err) {
		t.Fatalf("%s is left: %v", writeTest, err)
	}
}

// Story 59.09: on DSM the write-denied message adds DSM's own screens, naming the shared
// folder and the folder the data volume is (from the mount's root), in both languages.
func TestPreflightWriteCheckDSM(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	p := preflightFor(t, capBndDefault, "")
	p.platform = "dsm"
	if err := os.WriteFile(p.mountinfo, []byte("590 1 0:29 / / rw,relatime - overlay overlay rw\n"+
		"600 590 0:44 /@syno/docker/maktaba "+p.volume+" rw,relatime - btrfs /dev/mapper/cachedev_0 rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.volume, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p.volume, 0o755) })
	_, err := p.run()
	msg := refusalOf(t, err)
	for _, want := range []string{"Control Panel → Shared Folder → docker → Edit → Permissions",
		"File Station → docker/maktaba → Properties → Permission", "uid " + strconv.Itoa(p.uid),
		"لوحة التحكم ← المجلد المشترك ← docker"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q: %q", want, msg)
		}
	}
	p.platform = ""
	if msg := refusalOf(t, func() error { _, err := p.run(); return err }()); strings.Contains(msg, "On DSM") {
		t.Fatalf("not DSM, yet DSM's screens: %q", msg)
	}
}

func TestDSMDataFolder(t *testing.T) {
	dir := t.TempDir()
	mi := filepath.Join(dir, "mountinfo")
	for _, c := range []struct{ line, share, folder string }{
		{"600 590 0:44 /docker/maktaba /var/lib/maktaba rw - btrfs /dev/md2 rw", "docker", "docker/maktaba"},
		{"600 590 0:44 /@syno/SSD\\040apps/maktaba /var/lib/maktaba rw - btrfs /dev/md3 rw", "SSD apps", "SSD apps/maktaba"},
		{"600 590 0:44 /@docker/volumes/maktaba/_data /var/lib/maktaba rw - btrfs /dev/md2 rw", "docker", "docker/maktaba"},
		{"600 590 0:44 /docker/maktaba /elsewhere rw - btrfs /dev/md2 rw", "docker", "docker/maktaba"},
		{"600 590 8:1 /var/lib/docker/volumes/maktaba/_data /var/lib/maktaba rw - ext4 /dev/sda1 rw", "docker", "docker/maktaba"},
	} {
		if err := os.WriteFile(mi, []byte(c.line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if s, f := dsmDataFolder(mi, "/var/lib/maktaba"); s != c.share || f != c.folder {
			t.Errorf("%s: %q %q, want %q %q", c.line, s, f, c.share, c.folder)
		}
	}
}

// T06 (unit): a uid /etc/passwd lacks gets maktaba in <volume>/.nss and libnss_wrapper
// preloaded; the image's own maktaba (1000) no longer shadows it; a gid the group file has
// (100, users) is kept, one it lacks is added.
func TestPreflightNamesAnUnknownUID(t *testing.T) {
	for _, c := range []struct {
		uid, gid  int
		groupLine string
	}{{568, 568, "maktaba:x:568:"}, {1026, 100, "users:x:100:"}, {99, 100, "users:x:100:"}, {4321, 4321, "maktaba:x:4321:"}} {
		p := preflightFor(t, capBndDefault, sysRO)
		p.uid, p.gid = c.uid, c.gid
		env, err := p.run()
		if err != nil {
			t.Fatal(err)
		}
		pf, gf := filepath.Join(p.volume, nssDir, "passwd"), filepath.Join(p.volume, nssDir, "group")
		want := []string{"LD_PRELOAD=" + p.nssLib, "NSS_WRAPPER_PASSWD=" + pf, "NSS_WRAPPER_GROUP=" + gf}
		if !slices.Equal(env, want) {
			t.Fatalf("uid %d: env %v, want %v", c.uid, env, want)
		}
		pw, _ := os.ReadFile(pf)
		line := "maktaba:x:" + strconv.Itoa(c.uid) + ":" + strconv.Itoa(c.gid) + ":Maktaba:" + p.volume + ":/usr/sbin/nologin\n"
		if !strings.Contains(string(pw), line) || strings.Contains(string(pw), "maktaba:x:1000") || !strings.HasPrefix(string(pw), "root:x:0:0") {
			t.Fatalf("uid %d: passwd %q", c.uid, pw)
		}
		gr, _ := os.ReadFile(gf)
		if !strings.Contains(string(gr), c.groupLine+"\n") || strings.Count(string(gr), ":x:"+strconv.Itoa(c.gid)+":") != 1 {
			t.Fatalf("gid %d: group %q", c.gid, gr)
		}
	}
}

// a uid with a name (the image's 1000) needs nothing; nor does an image without the library.
func TestPreflightNamedUIDNeedsNothing(t *testing.T) {
	p := preflightFor(t, capBndDefault, sysRO)
	if env, err := p.run(); err != nil || env != nil {
		t.Fatalf("1000: %v %v", env, err)
	}
	if _, err := os.Stat(filepath.Join(p.volume, nssDir)); !os.IsNotExist(err) {
		t.Fatal("no .nss for a named uid")
	}
	p.uid, p.nssLib = 568, ""
	if env, err := p.run(); err != nil || env != nil {
		t.Fatalf("no library: %v %v", env, err)
	}
}

// the container command: a refusal prints its message and exits 78 before any bootstrap;
// the preflight's variables reach the command it runs.
func TestContainerRunPreflight(t *testing.T) {
	c, out := containerFor(t, "")
	c.preflight = func() ([]string, error) { return nil, &refusal{containerText("container.root_refused")} }
	code, err := c.run(context.Background(), nil)
	if err != nil || code != exitConfig {
		t.Fatalf("code %d err %v", code, err)
	}
	if !strings.Contains(out.String(), "does not run as root") || !strings.Contains(out.String(), "لا يعمل Maktaba") {
		t.Fatalf("out: %q", out.String())
	}
}

func TestWithEnv(t *testing.T) {
	got := withEnv([]string{"A=1", "LD_PRELOAD=/x.so", "B=2"}, "LD_PRELOAD=/y.so", "C=3")
	if want := []string{"A=1", "B=2", "LD_PRELOAD=/y.so", "C=3"}; !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
}
