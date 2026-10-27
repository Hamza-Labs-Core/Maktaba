package httperror_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// rawCause matches a 5xx problem whose detail is built from an error's text:
// httperror.Internal("x: "+err.Error()), httperror.BadGateway(err.Error()).
// The cause goes in Wrap (logged as http_error), the detail stays generic.
var rawCause = regexp.MustCompile(`httperror\.(Internal|BadGateway)\([^)]*\.Error\(\)`)

// rawCauseAllowed are the answers whose error text is the curated reason an
// admin (or a machine) acts on: a self-update's refused signature or checksum,
// the package manager's output, a mirror fetch the node logs and retries.
var rawCauseAllowed = map[string]bool{
	"internal/system/selfupdate.go": true,
	"internal/updates/mirror.go":    true,
}

func TestNoErrorTextInServerErrorDetail(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rawCauseAllowed[filepath.ToSlash(rel)] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if rawCause.MatchString(line) {
				t.Errorf("%s:%d: a 5xx detail carries an error's text; use .Wrap(err): %s",
					rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
