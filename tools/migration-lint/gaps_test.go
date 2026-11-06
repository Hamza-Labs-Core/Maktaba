package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Rule 7: every unused slot below the highest is a listed permanent gap (#643: 0176 and
// 0177 were unused on main but not listed), and a listed gap has no migration.
func TestPermanentGapsListed(t *testing.T) {
	files, err := listMigrations(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range gapCheck(repoMigrations, files) {
		t.Errorf("%s", v.Message)
	}

	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want int) {
		t.Helper()
		files, _ := listMigrations(dir)
		if got := gapCheck(dir, files); len(got) != want {
			t.Errorf("violations %v, want %d", got, want)
		}
	}
	for _, n := range []string{"0000", "0001", "0003", "0006"} {
		write(n+"_x.sql", "SELECT 1;\n")
	}
	gaps := func(bullets string) string {
		return "# M\n\n| `0000` | x |\n\n### Permanent gaps\n\n" + bullets + "\n---\n\n## Reservation\n\n- **0005**: not a gap list.\n"
	}
	write("MANIFEST.md", gaps("- **0002**: a.\n- **0004**: b.\n- **0005**: c.\n"))
	check(0)
	write("MANIFEST.md", gaps("- **0002**: a.\n- **0004**: b.\n"))
	check(1) // 0005 unlisted; the bullet after the section does not count
	write("MANIFEST.md", gaps("- **0002**: a.\n- **0003**: used.\n- **0004**: b.\n- **0005**: c.\n"))
	check(1) // 0003 listed but used
	write("MANIFEST.md", gaps("- **0002**: a.\n- **0004–0005**: a range.\n"))
	check(0) // a range bullet lists each slot in it
	write("0007_x.sqlite.sql", "SELECT 1;\n")
	write("MANIFEST.md", gaps("- **0002**: a.\n- **0004**: b.\n- **0005**: c.\n"))
	check(0) // a .sqlite.sql variant alone still fills its slot; the highest moves to 0007 with no gap
}
