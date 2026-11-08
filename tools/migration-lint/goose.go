package main

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// gooseCheck (check 8) refuses a file goose would refuse to parse: the `-- +goose` annotations
// must come in goose's order. `Up` first and once; `Down` at most once, after `Up`;
// `StatementBegin` only inside a section and never inside another block; `StatementEnd` only
// after a `StatementBegin`; no section starts and the file does not end inside a block.
//
// The other checks read the SQL text, not the annotations, so a file with a misplaced
// annotation passed them and failed only when goose ran it (0215_fp_roles on PR #925 opened
// a block twice).
func gooseCheck(file string, body []byte) []violation {
	var out []violation
	bad := func(line int, format string, args ...any) {
		out = append(out, violation{File: file, Rule: "goose-annotations",
			Message: fmt.Sprintf("line %d: ", line) + fmt.Sprintf(format, args...)})
	}

	section := "" // "", "Up" or "Down"
	seenUp, seenDown := false, false
	blockAt := 0 // line of the open StatementBegin, 0 when none
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		n++
		t := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(t, "--") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, "--"))
		if !strings.HasPrefix(rest, "+goose") {
			continue
		}
		switch strings.Join(strings.Fields(strings.TrimPrefix(rest, "+goose")), " ") {
		case "Up":
			switch {
			case blockAt > 0:
				bad(n, "+goose Up inside the block opened at line %d", blockAt)
			case seenUp:
				bad(n, "a second +goose Up")
			case seenDown:
				bad(n, "+goose Up after +goose Down")
			}
			seenUp, section = true, "Up"
		case "Down":
			switch {
			case blockAt > 0:
				bad(n, "+goose Down inside the block opened at line %d", blockAt)
			case seenDown:
				bad(n, "a second +goose Down")
			case !seenUp:
				bad(n, "+goose Down before +goose Up")
			}
			seenDown, section = true, "Down"
		case "StatementBegin":
			switch {
			case section == "":
				bad(n, "+goose StatementBegin before +goose Up or +goose Down")
			case blockAt > 0:
				bad(n, "+goose StatementBegin inside the block opened at line %d", blockAt)
			}
			blockAt = n
		case "StatementEnd":
			if blockAt == 0 {
				bad(n, "+goose StatementEnd with no open +goose StatementBegin")
			}
			blockAt = 0
		}
	}
	if blockAt > 0 {
		bad(blockAt, "+goose StatementBegin never closed by +goose StatementEnd")
	}
	if !seenUp {
		bad(1, "no +goose Up annotation")
	}
	return out
}
