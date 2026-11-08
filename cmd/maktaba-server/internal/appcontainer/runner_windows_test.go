//go:build windows

package appcontainer

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// These run in CI's plugins-sandbox job on windows-2022 (never a developer's or the
// owner's machine): real AppContainer profiles, real ACLs on the test's temp folders.

var procDeleteProfile = userenv.NewProc("DeleteAppContainerProfile")

// testProfile is a runner group made for one test, its profile deleted after it.
func testProfile(t *testing.T, group string) (string, *windows.SID) {
	t.Helper()
	profile := RunnerProfile(group)
	sid, err := SID(profile)
	if err != nil {
		t.Fatalf("the test's AppContainer profile: %v", err)
	}
	t.Cleanup(func() {
		if name, err := windows.UTF16PtrFromString(profile); err == nil {
			_, _, _ = procDeleteProfile.Call(uintptr(unsafe.Pointer(name)))
		}
	})
	return profile, sid
}

// aceOf answers the SID's allow entries on path (explicit or inherited), read by name.
func aceOf(t *testing.T, path string, sid *windows.SID) (found bool, inherited bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return false, false
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, uint32(i), &ace) != nil {
			t.Fatalf("%s: ACE %d", path, i)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		if (*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) {
			found = true
			inherited = inherited || ace.Header.AceFlags&windows.INHERITED_ACE != 0
		}
	}
	return found, inherited
}

func autoInherited(t *testing.T, path string) bool {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	return ctrl&windows.SE_DACL_AUTO_INHERITED != 0
}

func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil { //nolint:gosec // the test's temp folders
		t.Fatalf("mklink /J %s %s: %v %s", link, target, err, out)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// launchReal runs LaunchRunner with the real grant and revoke and a fake policy run and
// start: it answers the exit code, whether the runner started, and the launcher's log.
func launchReal(t *testing.T, group string, pol RunnerPolicy, extra ...string) (int, bool, string) {
	t.Helper()
	ap, run, hook := askPolicy, runFn, afterCheck
	t.Cleanup(func() { askPolicy, runFn, afterCheck = ap, run, hook })
	raw, _ := json.Marshal(pol)
	askPolicy = func(string, []string, io.Writer) ([]byte, int, error) { return raw, 0, nil }
	started := false
	runFn = func(string, string, []string, Options) (int, error) { started = true; return 0, nil }
	var errOut strings.Builder
	args := append([]string{"-group", group, "-run-dir", filepath.Join(os.Getenv("MAKTABA_DATA_DIR"), "run")}, extra...)
	code := LaunchRunner(append(args, "--", "python"), &errOut)
	return code, started, errOut.String()
}

// Story 54.23: a grant lands only on the folder that was checked. Between the last check
// and the grant, a process of the same user swaps the checked folder (or a folder above
// it) for a junction to a decoy it chose: the decoy and what it holds get no entry for the
// group's profile, and the start fails closed (no runner runs). Without a swap the grant
// reaches what the folder already holds, and stops at a junction inside it.
func TestAGrantLandsOnlyOnTheFolderThatWasChecked(t *testing.T) {
	for _, swap := range []string{"none", "leaf", "ancestor"} {
		t.Run(swap, func(t *testing.T) {
			dir := mustCanonical(t, t.TempDir())
			t.Setenv("MAKTABA_DATA_DIR", dir)
			lib := filepath.Join(dir, "lib")
			media := filepath.Join(lib, "media")
			show := filepath.Join(media, "season 1", "show.mkv")
			mustWrite(t, show)
			decoyLib := filepath.Join(dir, "decoylib")
			decoy := filepath.Join(decoyLib, "media")
			secret := filepath.Join(decoy, "secret.txt")
			mustWrite(t, secret)
			outside := filepath.Join(dir, "outside")
			mustWrite(t, filepath.Join(outside, "other.txt"))
			junction(t, filepath.Join(media, "escape"), outside)

			group := "ci-grant-" + swap
			_, sid := testProfile(t, group)
			ac := afterCheck
			t.Cleanup(func() { afterCheck = ac })
			afterCheck = func(step, p string) {
				if step != "grant" || pathKey(p) != pathKey(media) {
					return
				}
				switch swap {
				case "leaf":
					if err := os.Rename(media, media+".moved"); err != nil {
						t.Errorf("swap: %v", err)
					}
					junction(t, media, decoy)
				case "ancestor":
					if err := os.Rename(lib, lib+".moved"); err != nil {
						t.Errorf("swap: %v", err)
					}
					junction(t, lib, decoyLib)
				}
			}
			wn := walkNote
			t.Cleanup(func() { walkNote = wn })
			walkNote = func(p string, err error) { t.Logf("walk: %s: %v", p, err) }
			code, started, log := launchReal(t, group, RunnerPolicy{Read: []string{media}})
			if swap == "none" {
				t.Logf("DEBUG log: %s", log)
				for _, p := range []string{media, filepath.Dir(show), show} {
					out, _ := exec.Command("icacls", p).CombinedOutput() //nolint:gosec // the test's temp folder
					t.Logf("DEBUG %s", out)
				}
			}
			for _, p := range []string{decoy, secret, outside, filepath.Join(outside, "other.txt")} {
				if found, _ := aceOf(t, p, sid); found {
					t.Errorf("%s got an entry for the group's profile", p)
				}
			}
			if swap == "none" {
				if code != 0 || !started {
					t.Fatalf("exit %d, started %v: %s", code, started, log)
				}
				for _, p := range []string{media, filepath.Dir(show), show} {
					if found, _ := aceOf(t, p, sid); !found {
						t.Errorf("%s has no entry for the group's profile", p)
					}
				}
				if _, inherited := aceOf(t, show, sid); !inherited {
					t.Errorf("%s: the entry is not marked inherited", show)
				}
				// still auto-inherited: Windows' own propagation keeps treating it as a child
				if !autoInherited(t, show) {
					t.Errorf("%s lost its auto-inherited flag", show)
				}
				return
			}
			if code != RunnerSandboxExit || started {
				t.Fatalf("a swapped folder: exit %d, started %v: %s", code, started, log)
			}
			if !strings.Contains(log, "plugin_runner_sandbox_unavailable") {
				t.Fatalf("not logged: %s", log)
			}
		})
	}
}

// A revoke takes back only what was granted on the folder that was checked: a folder
// swapped for a junction to another folder where the group holds a grant leaves that
// grant alone, and the start fails closed. Without a swap the revoke takes the entry back
// from the folder and from what it holds.
func TestARevokeLandsOnlyOnTheFolderThatWasChecked(t *testing.T) {
	for _, swap := range []string{"none", "leaf", "ancestor"} {
		t.Run(swap, func(t *testing.T) {
			dir := mustCanonical(t, t.TempDir())
			t.Setenv("MAKTABA_DATA_DIR", dir)
			lib, otherLib := filepath.Join(dir, "lib"), filepath.Join(dir, "otherlib")
			old := filepath.Join(lib, "old")
			oldFile := filepath.Join(old, "a", "show.mkv")
			mustWrite(t, oldFile)
			other := filepath.Join(otherLib, "old")
			otherFile := filepath.Join(other, "keep.txt")
			mustWrite(t, otherFile)
			group := "ci-revoke-" + swap
			profile, sid := testProfile(t, group)
			for _, p := range []string{old, other} {
				if err := Grant(profile, p, false); err != nil {
					t.Fatalf("the earlier grant on %s: %v", p, err)
				}
			}
			grants := filepath.Join(dir, "grants.json")
			b, _ := json.Marshal([]GrantEntry{{Path: old}})
			if err := os.WriteFile(grants, b, 0o600); err != nil {
				t.Fatal(err)
			}
			ac := afterCheck
			t.Cleanup(func() { afterCheck = ac })
			afterCheck = func(step, p string) {
				if step != "revoke" || pathKey(p) != pathKey(old) {
					return
				}
				switch swap {
				case "leaf":
					if err := os.Rename(old, old+".moved"); err != nil {
						t.Errorf("swap: %v", err)
					}
					junction(t, old, other)
				case "ancestor":
					if err := os.Rename(lib, lib+".moved"); err != nil {
						t.Errorf("swap: %v", err)
					}
					junction(t, lib, otherLib)
				}
			}
			code, started, log := launchReal(t, group, RunnerPolicy{}, "-grants", grants)
			for _, p := range []string{other, otherFile} {
				if found, _ := aceOf(t, p, sid); !found {
					t.Errorf("%s lost the group's grant", p)
				}
			}
			if swap == "none" {
				if code != 0 || !started {
					t.Fatalf("exit %d, started %v: %s", code, started, log)
				}
				for _, p := range []string{old, filepath.Dir(oldFile), oldFile} {
					if found, _ := aceOf(t, p, sid); found {
						t.Errorf("%s still has the group's entry", p)
					}
				}
				return
			}
			if code != RunnerSandboxExit || started {
				t.Fatalf("a swapped folder: exit %d, started %v: %s", code, started, log)
			}
		})
	}
}

// The areas folder's grant reaches the files directly in it (the worker's areas.json) and
// none of its folders (the other groups' run folders); a folder below a grant whose DACL is
// protected inherits nothing; a grant on a folder that is a junction itself is refused.
func TestAGrantsWalkStopsWhereWindowsInheritanceStops(t *testing.T) {
	dir := mustCanonical(t, t.TempDir())
	profile, sid := testProfile(t, "ci-walk")
	areas := filepath.Join(dir, "areas")
	areasFile := filepath.Join(areas, "areas.json")
	otherRun := filepath.Join(areas, "net", "token")
	mustWrite(t, areasFile)
	mustWrite(t, otherRun)
	if err := GrantFiles(profile, areas); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{areas: true, areasFile: true, filepath.Dir(otherRun): false, otherRun: false} {
		if found, _ := aceOf(t, p, sid); found != want {
			t.Errorf("%s: an entry %v, want %v", p, found, want)
		}
	}

	media := filepath.Join(dir, "media")
	private := filepath.Join(media, "private")
	mustWrite(t, filepath.Join(private, "x.txt"))
	if out, err := exec.Command("icacls", private, "/inheritance:d").CombinedOutput(); err != nil { //nolint:gosec // the test's temp folder
		t.Fatalf("icacls: %v %s", err, out)
	}
	if err := Grant(profile, media, true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{private, filepath.Join(private, "x.txt")} {
		if found, _ := aceOf(t, p, sid); found {
			t.Errorf("%s (protected) got an entry", p)
		}
	}

	link := filepath.Join(dir, "link")
	junction(t, link, media)
	if err := Grant(profile, link, false); err == nil {
		t.Fatal("a junction was granted")
	}
	if err := Revoke(profile, link); err == nil {
		t.Fatal("a junction was revoked")
	}
}
