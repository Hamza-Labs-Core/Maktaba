package main

import (
	"path/filepath"
	"testing"
)

// A standby kit's file is named after a machine's id and stays in the switchover folder:
// a path, a traversal or anything but a UUID is refused.
func TestSealedKitPathTakesAMachinesIDOnly(t *testing.T) {
	state := t.TempDir()
	id := "09b891e9-3831-4952-af9c-ec1fd51a2b3c"
	p, err := sealedKitPath(state, id)
	if err != nil || filepath.Base(p) != "standby-"+id+".chunks" ||
		filepath.Base(filepath.Dir(p)) != "switchover" {
		t.Fatalf("a machine's id: %q %v", p, err)
	}
	for _, bad := range []string{"", "..", "../x", `..\x`, "a/b", `C:\x`, "/etc/passwd", "not-a-uuid",
		id + "/../../x", "09b891e9-3831-4952-af9c-ec1fd51a2b3c.."} {
		if p, err := sealedKitPath(state, bad); err == nil {
			t.Errorf("%q: %q", bad, p)
		}
	}
}

// The new home's notice is asked only at a machine's address as the cluster records it.
func TestHomeURLShape(t *testing.T) {
	for _, ok := range []string{"http://192.168.50.20:8080", "https://home.local:8443/", "http://studio",
		"http://[fd00::20]:8080"} {
		if !homeURLRe.MatchString(ok) {
			t.Errorf("refused %q", ok)
		}
	}
	for _, bad := range []string{"", "file:///etc/passwd", "http://user:pw@home:8080", "http://home:8080/api/x",
		"http://home:8080?x=1", "gopher://home", "http://home:8080#f", "http://home:8080/../x", "ftp://home"} {
		if homeURLRe.MatchString(bad) {
			t.Errorf("took %q", bad)
		}
	}
}
