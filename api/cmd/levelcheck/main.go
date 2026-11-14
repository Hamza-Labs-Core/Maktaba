// Command levelcheck checks shared/api/openapi.yaml against the current API level's
// snapshot (Epic 58 story 58.01), and with -write writes that snapshot.
//
//	go run ./cmd/levelcheck          (from api/) prints what an older app would get wrong
//	go run ./cmd/levelcheck -write   writes shared/api/levels/<compat.Level>.yaml
//
// -write refuses to overwrite a snapshot the document is not compatible with: an
// incompatible change raises compat.Level first, and the new level gets its own file.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/compat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/compat/levelcheck"
)

func main() {
	write := flag.Bool("write", false, "write shared/api/levels/<level>.yaml from shared/api/openapi.yaml")
	root := flag.String("root", "..", "the repository root")
	flag.Parse()
	if err := run(*root, *write); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root string, write bool) error {
	doc, err := os.ReadFile(filepath.Join(root, "shared", "api", "openapi.yaml"))
	if err != nil {
		return err
	}
	cur, err := levelcheck.Read(doc)
	if err != nil {
		return err
	}
	cur.Level = compat.Level
	snapPath := filepath.Join(root, "shared", "api", "levels", fmt.Sprintf("%d.yaml", compat.Level))
	var snap *levelcheck.Surface
	if b, err := os.ReadFile(snapPath); err == nil {
		if snap, err = levelcheck.Unmarshal(b); err != nil {
			return fmt.Errorf("%s: %w", snapPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if write {
		if snap != nil {
			if changes := levelcheck.Diff(snap, cur); len(changes) > 0 {
				return fmt.Errorf("%s: not overwritten, the document is not compatible with it (%d changes; raise compat.Level first)", snapPath, len(changes))
			}
		}
		b, err := levelcheck.Marshal(cur)
		if err != nil {
			return err
		}
		if err := os.WriteFile(snapPath, b, 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", snapPath)
		return nil
	}
	levels, err := os.ReadFile(filepath.Join(root, "docs", "api", "levels.md"))
	if err != nil {
		return err
	}
	if problems := levelcheck.Check(cur, compat.Level, snap, string(levels)); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		return errors.New("levelcheck: failed")
	}
	fmt.Printf("API level %d: shared/api/openapi.yaml is compatible with its snapshot\n", compat.Level)
	return nil
}
