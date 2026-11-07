package ports

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// notCalled are hosts the code names that no process calls: problem types and
// documents' links, identities checked inside a signature, placeholders.
var notCalled = map[string]string{
	"maktaba.tv":                          "problem type URIs and links in answers",
	"maktaba.dev":                         "links in security.txt",
	"maktaba.invalid":                     "a placeholder",
	"token.actions.githubusercontent.com": "the release signature's issuer, checked offline",
	"rekor.localhost":                     "the test Sigstore",
	"fulcio.localhost":                    "the test Sigstore",
	"cloud.test":                          "a test placeholder",
	"schemas.microsoft.com":               "an XML namespace (the Windows task)",
	"www.apple.com":                       "a plist DTD (the launchd job)",
	"customer-":                           "a Cloudflare Stream customer subdomain built from the account (api.cloudflare.com listed)",
	"status.maktaba.tv":                   "the cloud's status page in the web app's CSP: the browser reads it, never a server (Epic 50 story 50.11)",
}

var (
	// a URL in a string: "…", '…' (Python) or `…`; the quote is checked apart, so the
	// pattern starts with a literal and the scan of every file stays fast
	hostRe = regexp.MustCompile(`https?://([a-zA-Z0-9.-]+)`)
	ipRe   = regexp.MustCompile(`^[0-9.]+$`)
)

// scanned are the folders whose code runs in a Maktaba process, and the source files in
// them: the servers' Go code and the pipeline's Python code.
var scanned = []string{"api", "streaming", "cmd", "shared/components", "shared/go", "pipeline/src"}

// source: a file of code a process runs (not a test).
func source(p string) bool {
	switch {
	case strings.HasSuffix(p, ".go"):
		return !strings.HasSuffix(p, "_test.go")
	case strings.HasSuffix(p, ".py"):
		return true
	}
	return false
}

// #640: every internet host the servers' Go code and the pipeline's Python code name is a
// listed outbound call (or a name nothing calls), so a new one cannot be added without
// saying how it is turned off.
func TestOutboundHostsAreListed(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	listed := OutboundHosts()
	found := map[string][]string{}
	for _, dir := range scanned {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "testdata", "node_modules", "releaseverifytest", "__pycache__", "tests":
					return filepath.SkipDir
				}
				return nil
			}
			if !source(p) {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			text := string(b)
			for _, ix := range hostRe.FindAllStringSubmatchIndex(text, -1) {
				if ix[0] == 0 || !strings.ContainsRune("\"'`", rune(text[ix[0]-1])) {
					continue // not the start of a string
				}
				h := strings.ToLower(text[ix[2]:ix[3]])
				if h == "localhost" || strings.HasSuffix(h, ".local") || ipRe.MatchString(h) ||
					(!strings.Contains(h, ".") && h != "customer-") {
					continue // this machine, the LAN, an example address
				}
				rel, _ := filepath.Rel(root, p)
				found[h] = append(found[h], rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var missing []string
	for h, files := range found {
		if listed[h] || notCalled[h] != "" {
			continue
		}
		missing = append(missing, h+" ("+files[0]+")")
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("hosts the code calls that ports.Outbound does not list (add each with the switch that turns it off, or to notCalled when nothing calls it):\n  %s",
			strings.Join(missing, "\n  "))
	}
	for _, c := range Outbound {
		if c.Name == "" || c.What == "" || c.Off == "" || (c.By != "auto" && c.By != "admin") {
			t.Errorf("%+v: a call needs a name, what it is for, by auto or admin, and what turns it off", c)
		}
	}
}

func TestOffline(t *testing.T) {
	t.Setenv(EnvOffline, "1")
	if !Offline() {
		t.Fatal("MAKTABA_OFFLINE=1: not offline")
	}
	t.Setenv(EnvOffline, "")
	if Offline() {
		t.Fatal("MAKTABA_OFFLINE unset: offline")
	}
	for v, want := range map[string]bool{"": false, "0": false, "off": false, "1": true, "true": true, "ON": true, " yes ": true} {
		if got := OfflineFrom(func(string) string { return v }); got != want {
			t.Errorf("%q: %v", v, got)
		}
	}
}
