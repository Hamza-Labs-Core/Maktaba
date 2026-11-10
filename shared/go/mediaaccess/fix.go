package mediaaccess

import (
	"os"
	"os/user"
	"strconv"
	"strings"
)

// Platforms the fix text knows (story 59.04's detection: MAKTABA_PLATFORM or what the
// container sees). Any other value gets the general text.
const (
	PlatformDSM     = "dsm"
	PlatformTrueNAS = "truenas"
	PlatformUnraid  = "unraid"
)

// EnvHostPlatform carries the platform maktaba-server detected at its start (story 59.04,
// hostinfo.Detect) to the roles it runs, so a role never detects again. It is a name of
// its own: setting MAKTABA_PLATFORM instead would make the next detection say it came
// from the environment.
const EnvHostPlatform = "MAKTABA_HOST_PLATFORM"

// PlatformFromEnv is the platform a role was given: the one detected at the start, else
// MAKTABA_PLATFORM (a role run on its own), else "".
func PlatformFromEnv() string {
	if p := strings.TrimSpace(os.Getenv(EnvHostPlatform)); p != "" {
		return p
	}
	return strings.TrimSpace(os.Getenv("MAKTABA_PLATFORM"))
}

// RunsAs is the user Maktaba runs as: the page says "user 568, group 568".
type RunsAs struct {
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
	Name string `json:"name"`
}

// CurrentUser is this process's user and group (-1 where the system has none: Windows),
// with the user's name when the system knows one.
func CurrentUser() RunsAs {
	r := RunsAs{UID: os.Getuid(), GID: os.Getgid()}
	if r.UID >= 0 {
		if u, err := user.LookupId(strconv.Itoa(r.UID)); err == nil {
			r.Name = u.Username
		}
	}
	return r
}

// Fix is the sentence that tells the household what to change, as an i18n key
// (library.fix.<platform>) and its parameters; every parameter is always present.
type Fix struct {
	Key    string            `json:"key"`
	Params map[string]string `json:"params"`
}

// FixFor is the fix for a root on platform, in that NAS's own words: on DSM the shared
// folder (/volume1/<share>), on TrueNAS the dataset (/mnt/<pool>/<dataset>), on Unraid the
// share (/mnt/user/<share>); where the path says neither, the path itself stands in.
func FixFor(platform, path string, who RunsAs) Fix {
	uid := strconv.Itoa(who.UID)
	name := who.Name
	if name == "" {
		name = uid
	}
	p := map[string]string{"share": path, "dataset": path, "name": name, "uid": uid, "path": path}
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformDSM:
		if s := dsmShare(path); s != "" {
			p["share"] = s
		}
		return Fix{Key: "library.fix.dsm", Params: p}
	case PlatformTrueNAS:
		if d := truenasDataset(path); d != "" {
			p["dataset"] = d
		}
		return Fix{Key: "library.fix.truenas", Params: p}
	case PlatformUnraid:
		if s := unraidShare(path); s != "" {
			p["share"] = s
		}
		return Fix{Key: "library.fix.unraid", Params: p}
	}
	return Fix{Key: "library.fix.other", Params: p}
}

func parts(path string) []string {
	var out []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// dsmShare: /volume1/Movies/... → Movies.
func dsmShare(path string) string {
	ps := parts(path)
	if len(ps) >= 2 && strings.HasPrefix(ps[0], "volume") {
		return ps[1]
	}
	return ""
}

// truenasDataset: /mnt/tank/media → tank/media (the pool and the path under it).
func truenasDataset(path string) string {
	ps := parts(path)
	if len(ps) >= 3 && ps[0] == "mnt" {
		return strings.Join(ps[1:], "/")
	}
	return ""
}

// unraidShare: /mnt/user/Movies/... (or a disk's /mnt/disk1/Movies/...) → Movies.
func unraidShare(path string) string {
	ps := parts(path)
	if len(ps) >= 3 && ps[0] == "mnt" {
		return ps[2]
	}
	return ""
}
