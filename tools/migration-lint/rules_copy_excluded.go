package main

// cluster_copy_excluded() keeps every table (rule copy-excluded-keeps).
//
// The function lists the tables a machine's copy of the home leaves out (secrets, the
// machines' own rows, each database's own). Each migration that adds such a table redefines
// it in full, starting from the newest definition. Two pull requests written in parallel
// each start from the definition on main when they were written, so the one merged second
// silently drops the tables the first added. In slot order, this rule checks that:
//
//   - a redefinition's Up section lists every table the previous definition listed, unless
//     the migration names it on a line "-- cluster_copy_excluded drops: <table>[, <table>]"
//     (the table is gone, or copied from now on);
//   - a redefinition's Down section, when it redefines the function, lists exactly the
//     previous definition's tables, so a rollback restores that definition.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	reCopyExcludedDef  = regexp.MustCompile(`(?is)FUNCTION\s+cluster_copy_excluded\s*\(\s*\)`)
	reCopyExcludedBody = regexp.MustCompile(`(?is)FUNCTION\s+cluster_copy_excluded\s*\(\s*\).*?\bARRAY\s*\[(.*?)\]`)
	reQuotedName       = regexp.MustCompile(`'([A-Za-z0-9_]+)'`)
	reCopyExcludedDrop = regexp.MustCompile(`(?im)^\s*--\s*cluster_copy_excluded\s+drops:\s*(.+)$`)
)

// copyExcludedTables answers the tables a section's definition lists, or nil when the
// section does not define the function.
func copyExcludedTables(section []byte) map[string]bool {
	s := stripComments(section)
	if !reCopyExcludedDef.Match(s) {
		return nil
	}
	out := map[string]bool{}
	for _, m := range reCopyExcludedBody.FindAllSubmatch(s, -1) {
		for _, n := range reQuotedName.FindAllSubmatch(m[1], -1) {
			out[string(n[1])] = true
		}
	}
	return out
}

func downSection(body []byte) []byte {
	if loc := reGooseDown.FindIndex(body); loc != nil {
		return body[loc[1]:]
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// copyExcludedCheck is the rule over the Postgres migrations (files in slot order).
func copyExcludedCheck(files []string, read func(string) ([]byte, error)) []violation {
	var out []violation
	var prev map[string]bool
	prevFile := ""
	for _, f := range files {
		if strings.HasSuffix(f, ".sqlite.sql") {
			continue
		}
		body, err := read(f)
		if err != nil {
			out = append(out, violation{File: f, Rule: "copy-excluded-keeps", Message: err.Error()})
			continue
		}
		up := copyExcludedTables(upSection(body))
		if up == nil {
			continue
		}
		if prev != nil {
			dropped := map[string]bool{}
			for _, m := range reCopyExcludedDrop.FindAllSubmatch(body, -1) {
				for _, n := range strings.Split(string(m[1]), ",") {
					dropped[strings.TrimSpace(n)] = true
				}
			}
			var lost []string
			for _, t := range sortedKeys(prev) {
				if !up[t] && !dropped[t] {
					lost = append(lost, t)
				}
			}
			if len(lost) > 0 {
				out = append(out, violation{File: f, Rule: "copy-excluded-keeps", Message: fmt.Sprintf(
					"cluster_copy_excluded() loses %s, which %s listed: start from the newest "+
						"definition (or name it on a line \"-- cluster_copy_excluded drops: <table>\")",
					strings.Join(lost, ", "), filepath.Base(prevFile))})
			}
			if down := copyExcludedTables(downSection(body)); down != nil {
				var missing, extra []string
				for _, t := range sortedKeys(prev) {
					if !down[t] {
						missing = append(missing, t)
					}
				}
				for _, t := range sortedKeys(down) {
					if !prev[t] {
						extra = append(extra, t)
					}
				}
				if len(missing) > 0 || len(extra) > 0 {
					out = append(out, violation{File: f, Rule: "copy-excluded-keeps", Message: fmt.Sprintf(
						"its Down does not restore %s's cluster_copy_excluded() (missing: %s; not "+
							"there: %s)", filepath.Base(prevFile), orNone(missing), orNone(extra))})
				}
			}
		}
		prev, prevFile = up, f
	}
	return out
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}
