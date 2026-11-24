package main

// A failed update is undone by its migrations' own Down scripts. The whole pre-update dump
// goes back only when they fail (cmd/maktaba-server/internal/updater/failedback.go). On
// 2026-11-23 and 2026-11-24 the database step put a 34 GB dump back instead, about three
// hours down each time. These rules keep every migration undoable by its Down script:
//
//  9. Down script: every Postgres migration has a Down section with a statement. In a
//     NO TRANSACTION file, a failure part way leaves whatever ran, and its Down script then
//     runs on that (`maktaba-api migrate undo-failed`). So every Down statement must run on
//     any part of the Up: harmless when what it undoes is not there (the same guards as
//     rule 5: DROP … IF EXISTS, ALTER TABLE … DROP COLUMN IF EXISTS, CREATE OR REPLACE, a
//     DO block that checks first), from slot losslessFrom. The rehearsal runs each one
//     after every partial Up (api/migrate_down_e2e_test.go).
// 10. Lossless Up (slots from losslessFrom): an Up statement that deletes, overwrites,
//     recasts or drops data (DELETE, UPDATE … SET, TRUNCATE, ALTER … TYPE, DROP TABLE,
//     COLUMN, SEQUENCE or SCHEMA) loses data its Down script cannot give back. A failed
//     update across it would put the whole dump back. Such a file must say which it is, in
//     a comment of its Up section:
//
//     -- +maktaba down-restores: <how its Down script gives the data back>
//
//     (a backfill of a column the Down drops, a seed or setting the Down sets back: the
//     update's way back then takes it as lossless), or
//
//     -- +maktaba lossy-up: <why a failed update across it may put the dump back>
//
//     Keep the data instead when you can: copy it aside, rename rather than drop, backfill
//     in the pipeline.

import (
	"fmt"
	"regexp"
	"strings"
)

// losslessFrom is the first slot rule 10, and rule 9's statements after a partial Up,
// apply to: the ones before are released and append-only, and every home is past them
// (nine NO TRANSACTION files before it add a constraint back unguarded in their Down,
// 0003 to 0224: their Down would fail after a part of their Up that never dropped it).
const losslessFrom = 237

var (
	// the statements of an Up script that lose data (the runtime's list:
	// cmd/maktaba-server/internal/updater/schemadown.go, lossy and dropLosesData)
	reLossy = []*regexp.Regexp{
		regexp.MustCompile(`(?is)^\s*TRUNCATE\b`),
		regexp.MustCompile(`(?is)^\s*DELETE\s+FROM\b`),
		regexp.MustCompile(`(?is)^\s*UPDATE\s+["A-Za-z_].*?\bSET\b`),
		regexp.MustCompile(`(?is)\bALTER\s+(?:COLUMN\s+)?(?:"[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s+(?:SET\s+DATA\s+)?TYPE\b`),
		regexp.MustCompile(`(?is)^\s*DROP\s+(?:TABLE|SEQUENCE|SCHEMA)\b`),
		regexp.MustCompile(`(?is)\bDROP\s+COLUMN\b`),
		regexp.MustCompile(`(?is)^\s*WITH\b.*\b(?:DELETE\s+FROM|UPDATE\s+\S+.*?\bSET)\b`),
	}
	reDownRestores = regexp.MustCompile(`(?im)^\s*--\s*\+maktaba\s+down-restores:\s*\S`)
	reLossyUp      = regexp.MustCompile(`(?im)^\s*--\s*\+maktaba\s+lossy-up:\s*\S`)
)

// downCheck is rules 9 and 10 for one Postgres file.
func downCheck(file string, body []byte) []violation {
	if strings.HasSuffix(file, ".sqlite.sql") {
		return nil
	}
	var out []violation
	down := downSection(body)
	stmts := splitSQL(stripComments(down))
	if len(stmts) == 0 {
		out = append(out, violation{File: file, Rule: "down-script",
			Message: "no Down script: a failed update is undone by its migrations' Down scripts (the whole dump goes " +
				"back only when they fail), so every migration has a `-- +goose Down` section with its statements"})
	}
	if reNoTx.Match(body) && slotOf(file) >= losslessFrom {
		dropped := map[string]bool{} // constraints dropped IF EXISTS so far: adding them again is harmless
		for _, st := range stmts {
			for _, m := range reDropConstr.FindAllStringSubmatch(st, -1) {
				dropped[strings.Trim(m[1], `"`)] = true
			}
			if why := notIdempotent(st, dropped); why != "" {
				out = append(out, violation{File: file, Rule: "down-after-partial-up",
					Message: fmt.Sprintf("a NO TRANSACTION file's Down script runs after any part of its Up (a failure "+
						"part way: `migrate undo-failed`), so each statement must be harmless when what it undoes is "+
						"not there: %s — statement: %q", why, oneLine(st, 100))})
			}
		}
	}
	if slotOf(file) >= losslessFrom && !reDownRestores.Match(body) && !reLossyUp.Match(body) {
		for _, st := range splitSQL(stripComments(upSection(body))) {
			if lossyStatement(st) {
				out = append(out, violation{File: file, Rule: "lossless-up",
					Message: fmt.Sprintf("this Up statement deletes, overwrites, recasts or drops data its Down script "+
						"cannot give back, so a failed update across it would put the whole dump back. Keep the data "+
						"(copy it aside, rename rather than drop), or say in a comment `-- +maktaba down-restores: "+
						"<how>` when the Down script gives it back, or `-- +maktaba lossy-up: <why>` — statement: %q",
						oneLine(st, 100))})
			}
		}
	}
	return out
}

// lossyStatement reports whether st (one statement of an Up script) loses data. A
// function's or a DO block's body (dollar-quoted) only defines or checks: judged by the
// statement around it.
func lossyStatement(st string) bool {
	if regexp.MustCompile(`(?is)^\s*(?:DO\b|CREATE\s+(?:OR\s+REPLACE\s+)?(?:FUNCTION|PROCEDURE|TRIGGER)\b)`).MatchString(st) {
		return false
	}
	for _, re := range reLossy {
		if re.MatchString(st) {
			return true
		}
	}
	return false
}
