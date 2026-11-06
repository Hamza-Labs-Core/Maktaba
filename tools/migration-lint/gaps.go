package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Rule 7, permanent gaps: a slot below the highest one that has no migration can never
// be filled (goose runs without --allow-missing, so on a database already past it the
// migration is out of order and goose refuses it). Every such slot is listed under
// MANIFEST.md's "### Permanent gaps" heading as a "- **NNNN**" (or "- **NNNN–NNNN**")
// bullet, so no PR claims it, and every listed slot really has no migration.

var reGapBullet = regexp.MustCompile("^-\\s+\\*\\*(\\d{4})(?:\\s*[–-]\\s*(\\d{4}))?\\*\\*")

// permanentGaps are the slots MANIFEST.md lists under "### Permanent gaps".
func permanentGaps(md []byte) map[int]bool {
	gaps := map[int]bool{}
	in := false
	for _, line := range strings.Split(string(md), "\n") {
		if strings.HasPrefix(line, "#") {
			in = strings.TrimSpace(strings.TrimLeft(line, "#")) == "Permanent gaps"
			continue
		}
		if !in {
			continue
		}
		if strings.TrimSpace(line) == "---" {
			in = false
			continue
		}
		if m := reGapBullet.FindStringSubmatch(line); m != nil {
			lo, _ := strconv.Atoi(m[1])
			hi := lo
			if m[2] != "" {
				hi, _ = strconv.Atoi(m[2])
			}
			for n := lo; n <= hi; n++ {
				gaps[n] = true
			}
		}
	}
	return gaps
}

// gapCheck is rule 7 over the migrations of dir and its MANIFEST.md.
func gapCheck(dir string, files []string) []violation {
	manifest := filepath.Join(dir, "MANIFEST.md")
	md, err := os.ReadFile(manifest)
	if err != nil {
		return []violation{{File: manifest, Rule: "permanent-gaps", Message: err.Error()}}
	}
	gaps := permanentGaps(md)
	used := map[int]bool{}
	highest := -1
	for _, f := range files {
		s := slotOf(f)
		used[s] = true
		if s > highest {
			highest = s
		}
	}
	var out []violation
	for s := 0; s < highest; s++ {
		if !used[s] && !gaps[s] {
			out = append(out, violation{File: manifest, Rule: "permanent-gaps", Message: fmt.Sprintf(
				"slot %04d has no migration but is below the highest slot (%04d): list it under "+
					"\"### Permanent gaps\" as \"- **%04d**: …\" (it can never be filled)", s, highest, s)})
		}
	}
	for s := range gaps {
		if used[s] {
			out = append(out, violation{File: manifest, Rule: "permanent-gaps", Message: fmt.Sprintf(
				"slot %04d is listed as a permanent gap but has a migration", s)})
		}
	}
	return out
}
