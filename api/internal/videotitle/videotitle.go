// Package videotitle holds the one rule for the title a video shows when it
// has no metadata title: its file name without the final extension.
//
// The stored data does not change: videos.title stays NULL for a video with
// no metadata, and search keeps matching videos.filename. Only what a list,
// card or label shows goes through here, in Go (Display) or in SQL (SQL).
// Views that are about the file itself (paths, duplicates, file details)
// keep showing the raw filename.
//
// An extension is a final "." followed by one or more ASCII letters or
// digits, after a non-empty stem: "a.b.mkv" shows "a.b"; "notes", ".mp4" and
// "x." stay as they are. The web, Android and Apple apps carry the same rule
// in their own title helper, for the few places they fall back by themselves.
package videotitle

import "strings"

// extChars are the characters an extension is made of. SQL uses the same
// set through rtrim, so Go and SQL agree on every name.
const extChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// StripExt returns filename without its final extension, or filename
// unchanged when it has none.
func StripExt(filename string) string {
	r := strings.TrimRight(filename, extChars)
	// r ends at the dot before the extension: it must be longer than the
	// dot alone (a stem exists) and shorter than the name (the extension is
	// not empty).
	if len(r) > 1 && len(r) < len(filename) && r[len(r)-1] == '.' {
		return r[:len(r)-1]
	}
	return filename
}

// Display is the title a video shows: its title when it has one (not blank),
// else its filename without the extension.
func Display(title, filename string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	return StripExt(filename)
}

// SQL returns an expression for the title a video shows, given its title and
// filename columns (for example SQL("v.title", "v.filename")): the title when
// it is not blank, else the filename without its extension. It is NULL only
// when both columns are NULL, so it can sit inside another COALESCE.
//
// It uses only trim, rtrim, substr, length and CASE, which Postgres and SQLite
// both have, so the same text runs on either home.
func SQL(titleCol, filenameCol string) string {
	return "COALESCE(CASE WHEN trim(" + titleCol + ") <> '' THEN " + titleCol + " END, " +
		StripExtSQL(filenameCol) + ")"
}

// StripExtSQL returns an expression for filenameCol without its extension,
// the SQL form of StripExt.
func StripExtSQL(filenameCol string) string {
	r := "rtrim(" + filenameCol + ", '" + extChars + "')"
	return "(CASE WHEN length(" + r + ") > 1" +
		" AND length(" + r + ") < length(" + filenameCol + ")" +
		" AND substr(" + r + ", length(" + r + "), 1) = '.'" +
		" THEN substr(" + r + ", 1, length(" + r + ") - 1)" +
		" ELSE " + filenameCol + " END)"
}
