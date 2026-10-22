package ffmpeg

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// goos is the system this runs on (tests replace it).
var goos = runtime.GOOS

// OSRelease is what /etc/os-release says of the distribution.
type OSRelease struct {
	ID, IDLike, VersionID, PrettyName string
}

// readOSRelease reads /etc/os-release (else /usr/lib/os-release); nothing off Linux.
func readOSRelease() OSRelease {
	var r OSRelease
	if goos != "linux" {
		return r
	}
	f, err := os.Open("/etc/os-release")
	if err != nil {
		if f, err = os.Open("/usr/lib/os-release"); err != nil {
			return r
		}
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		} else {
			v = strings.Trim(v, `"'`)
		}
		switch k {
		case "ID":
			r.ID = strings.ToLower(v)
		case "ID_LIKE":
			r.IDLike = strings.ToLower(v)
		case "VERSION_ID":
			r.VersionID = v
		case "PRETTY_NAME":
			r.PrettyName = v
		}
	}
	return r
}

// InstallCommand is the command that installs the ffmpeg Maktaba needs on this
// distribution, with ffprobe beside it, or "" for one it does not know:
//
//	Debian, Ubuntu and theirs   apt: the distribution's ffmpeg has libx264
//	Fedora                      dnf from RPM Fusion: Fedora's own ffmpeg-free has no libx264
//	RHEL, Alma, Rocky, CentOS   dnf from RPM Fusion, which needs EPEL and CRB (EPEL's crb)
//	openSUSE                    zypper from Packman: openSUSE's own ffmpeg has no libx264
//	Arch and theirs             pacman: the distribution's ffmpeg has libx264
func InstallCommand(r OSRelease) string {
	ids := append([]string{r.ID}, strings.Fields(r.IDLike)...)
	has := func(id ...string) bool {
		return slices.ContainsFunc(ids, func(s string) bool { return slices.Contains(id, s) })
	}
	major, _, _ := strings.Cut(r.VersionID, ".")
	release, _ := strconv.Atoi(major)
	switch {
	case has("debian", "ubuntu"):
		return "sudo apt install ffmpeg"
	case has("arch"):
		return "sudo pacman -S --needed ffmpeg"
	// Fedora and its derivatives number their releases from 30 up (40, 43); RHEL's
	// clones say "fedora" too, with the RHEL release (8, 9, 10); Amazon Linux too,
	// with its year (2023), for which RPM Fusion has nothing
	case has("fedora") && release >= 30 && release < 1000:
		return fmt.Sprintf("sudo dnf install https://mirrors.rpmfusion.org/free/fedora/rpmfusion-free-release-%d.noarch.rpm"+
			" && sudo dnf install --allowerasing ffmpeg", release)
	case has("rhel", "centos", "rocky", "almalinux", "ol") && release >= 8:
		return fmt.Sprintf("sudo dnf install https://dl.fedoraproject.org/pub/epel/epel-release-latest-%d.noarch.rpm"+
			" https://mirrors.rpmfusion.org/free/el/rpmfusion-free-release-%d.noarch.rpm"+
			" && sudo crb enable && sudo dnf install --allowerasing ffmpeg", release, release)
	case has("opensuse-tumbleweed", "opensuse-slowroll", "opensuse-leap"):
		var dir string
		switch {
		case r.ID == "opensuse-tumbleweed":
			dir = "openSUSE_Tumbleweed"
		case r.ID == "opensuse-slowroll":
			dir = "openSUSE_Slowroll"
		case r.ID == "opensuse-leap" && r.VersionID != "":
			dir = "openSUSE_Leap_" + r.VersionID
		default:
			return ""
		}
		return "sudo zypper addrepo -cfp 90 https://ftp.gwdg.de/pub/linux/misc/packman/suse/" + dir + "/ packman" +
			" && sudo zypper --gpg-auto-import-keys refresh" +
			" && sudo zypper dist-upgrade --from packman --allow-vendor-change" +
			" && sudo zypper install --from packman ffmpeg"
	}
	return ""
}

// installNote is how to get the ffmpeg Maktaba needs, after a problem: on Linux the
// distribution's command (ffmpeg_install), or what to look for on one this does not
// know (ffmpeg_install_other). An ffmpeg too old where the distribution's own package
// is the one installed (apt, pacman) is not fixed by installing it again: then a newer
// distribution or another ffmpeg (ffmpeg_old_distro). Nothing off Linux (the app
// carries its ffmpeg there) or when a setting names a program that is not there (the
// setting is what to fix).
func installNote(r OSRelease, problem string) *Note {
	if goos != "linux" || problem == "ffmpeg_not_found" {
		return nil
	}
	need := fmt.Sprintf("%d.%d", MinVersion[0], MinVersion[1])
	distro := r.PrettyName
	if distro == "" {
		distro = r.ID
	}
	cmd := InstallCommand(r)
	if problem == "ffmpeg_old" && (strings.HasPrefix(cmd, "sudo apt ") || strings.HasPrefix(cmd, "sudo pacman ")) {
		return note("ffmpeg_old_distro", fmt.Sprintf("%s packages an ffmpeg older than %s, so installing it again does not help: "+
			"upgrade the distribution, or name a newer ffmpeg in server.toml ([media] ffmpeg).", distro, need), "distro", distro, "need", need)
	}
	if cmd != "" {
		return note("ffmpeg_install", fmt.Sprintf("On %s, this installs the ffmpeg Maktaba needs: %s", distro, cmd),
			"distro", distro, "command", cmd)
	}
	return note("ffmpeg_install_other", fmt.Sprintf("Install your distribution's ffmpeg: FFmpeg %s or later with the libx264 "+
		"encoder (some distributions keep it in an extra repository), or name one in server.toml ([media] ffmpeg).", need),
		"need", need)
}
