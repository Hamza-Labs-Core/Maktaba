// Package adopt moves a Maktaba this machine already runs outside the desktop
// app into the app (Epic 38 story 38.13, specs/epics/38-windows-app/
// move-an-install.md): it finds it (a scheduled task that runs maktaba-server
// serve, directly, through Maktaba's own launcher or from a script; the node
// installer's tasks; a server started by hand), stops it, carries its settings,
// keys, node config and files into the app (an SQLite database is copied; a
// Postgres stays where it is, its url carried), and once the app's server was
// verified removes the old task. The old folder is kept, and the old task's XML
// too: Undo brings it back.
package adopt

import (
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
)

// Task is one scheduled task's first Exec action.
type Task struct {
	Name    string // \Folder\Name
	Command string
	Args    string
	WorkDir string
	Enabled bool
	// UserID is whom its first principal runs as: a SID, DOMAIN\name or a
	// bare name, as Task Scheduler wrote it ("" when it names none).
	UserID string
}

type taskXML struct {
	RegistrationInfo struct {
		URI string `xml:"URI"`
	} `xml:"RegistrationInfo"`
	Principals struct {
		Principal []struct {
			UserID string `xml:"UserId"`
		} `xml:"Principal"`
	} `xml:"Principals"`
	Settings struct {
		Enabled *bool `xml:"Enabled"`
	} `xml:"Settings"`
	Actions struct {
		Exec []struct {
			Command          string `xml:"Command"`
			Arguments        string `xml:"Arguments"`
			WorkingDirectory string `xml:"WorkingDirectory"`
		} `xml:"Exec"`
	} `xml:"Actions"`
}

// nameComment is the `<!-- \Name -->` before each task of `schtasks /Query /XML ONE`.
var nameComment = regexp.MustCompile(`^\s*(\\.*?)\s*$`)

// ParseTasks reads `schtasks /Query /XML ONE`: a <Tasks> document holding every
// task the user can read, each after a comment with its name. Its bytes are the
// console's code page however the XML declares itself, so the declaration is
// ignored.
func ParseTasks(raw []byte) ([]Task, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	var out []Task
	name := ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		switch t := tok.(type) {
		case xml.Comment:
			if m := nameComment.FindStringSubmatch(string(t)); m != nil {
				name = m[1]
			}
		case xml.StartElement:
			if t.Name.Local != "Task" {
				continue
			}
			var tx taskXML
			if err := dec.DecodeElement(&tx, &t); err != nil {
				return out, err
			}
			task := Task{Name: name, Enabled: tx.Settings.Enabled == nil || *tx.Settings.Enabled}
			if task.Name == "" {
				task.Name = tx.RegistrationInfo.URI
			}
			if len(tx.Principals.Principal) > 0 {
				task.UserID = strings.TrimSpace(tx.Principals.Principal[0].UserID)
			}
			if len(tx.Actions.Exec) > 0 {
				e := tx.Actions.Exec[0]
				task.Command = strings.Trim(strings.TrimSpace(e.Command), `"`)
				task.Args = strings.TrimSpace(e.Arguments)
				task.WorkDir = strings.Trim(strings.TrimSpace(e.WorkingDirectory), `"`)
			}
			out = append(out, task)
			name = ""
		}
	}
}

// SplitArgs splits a Windows command line the way the C runtime does (quotes
// group, a backslash escapes a quote), enough for task arguments.
func SplitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	in, have := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && s[i+1] == '"':
			cur.WriteByte('"')
			have = true
			i++
		case c == '"':
			in = !in
			have = true
		case (c == ' ' || c == '\t') && !in:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}
