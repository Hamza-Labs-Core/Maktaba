package symbols

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// Frame is one crash frame as the service admin reads it: as the app sent it, and its
// place in the code when the binary's table is here.
type Frame struct {
	Raw    string  `json:"raw"`
	InApp  bool    `json:"in_app"`
	Symbol *Symbol `json:"symbol,omitempty"`
}

// Crash is one of a report's crashes (a Sentry event, 53.14), top frame first.
type Crash struct {
	Type   string  `json:"type"`
	Value  string  `json:"value,omitempty"`
	Frames []Frame `json:"frames"`
	// Missing are the binaries its frames name whose tables are not here (their
	// release's dSYMs were never uploaded)
	Missing []Image `json:"missing,omitempty"`
}

// Image is a binary a crash names.
type Image struct {
	CodeFile string `json:"code_file"`
	DebugID  string `json:"debug_id"`
}

// Finder is where tables are looked up by debug id (nil, nil: none).
type Finder interface {
	Find(ctx context.Context, debugID string) (*Table, error)
}

// event is the part of a Sentry event read here.
type event struct {
	Exception struct {
		Values []struct {
			Type       string `json:"type"`
			Value      string `json:"value"`
			Stacktrace struct {
				Frames []struct {
					Function string `json:"function"`
					Package  string `json:"package"`
					InApp    bool   `json:"in_app"`
				} `json:"frames"`
			} `json:"stacktrace"`
		} `json:"values"`
	} `json:"exception"`
	DebugMeta struct {
		Images []struct {
			Type     string `json:"type"`
			CodeFile string `json:"code_file"`
			DebugID  string `json:"debug_id"`
		} `json:"images"`
	} `json:"debug_meta"`
}

// MaxCrashes is the most crashes read from one report (the home keeps 5).
const MaxCrashes = 5

// Symbolicate reads a report's crashes against the tables find has. A crash that is not an
// event is skipped; a frame whose binary has no table keeps only its raw form. Tables are
// looked up once each.
func Symbolicate(ctx context.Context, crashes []json.RawMessage, find Finder) []Crash {
	tables := map[string]*Table{}
	looked := map[string]bool{}
	table := func(id string) *Table {
		if !looked[id] {
			looked[id] = true
			if find != nil {
				if t, err := find.Find(ctx, id); err == nil {
					tables[id] = t
				}
			}
		}
		return tables[id]
	}
	out := []Crash{}
	for i, raw := range crashes {
		if i >= MaxCrashes {
			break
		}
		var e event
		if json.Unmarshal(raw, &e) != nil || len(e.Exception.Values) == 0 {
			continue
		}
		images := map[string]string{}
		for _, im := range e.DebugMeta.Images {
			if id := NormalizeID(im.DebugID); im.CodeFile != "" && validID(id) {
				images[im.CodeFile] = id
			}
		}
		ex := e.Exception.Values[0]
		c := Crash{Type: ex.Type, Value: ex.Value, Frames: []Frame{}}
		missing := map[string]bool{}
		fr := ex.Stacktrace.Frames
		// Sentry's frames are oldest first; the admin reads the top first
		for k := len(fr) - 1; k >= 0; k-- {
			f := fr[k]
			x := Frame{Raw: f.Function, InApp: f.InApp}
			bin, off, ok := splitFrame(f.Function, f.Package)
			if id := images[bin]; ok && id != "" {
				if t := table(id); t != nil {
					// a caller's frame is its return address: the call is the byte before it
					if k != len(fr)-1 && off > 0 {
						off--
					}
					if s, found := t.Lookup(off); found {
						x.Symbol = &s
					}
				} else if !missing[id] {
					missing[id] = true
					c.Missing = append(c.Missing, Image{CodeFile: bin, DebugID: id})
				}
			}
			c.Frames = append(c.Frames, x)
		}
		out = append(out, c)
	}
	return out
}

// splitFrame reads "Binary Name + 0x1a2b" (the apps' unsymbolicated frame) as its binary
// and offset.
func splitFrame(function, pkg string) (string, uint64, bool) {
	i := strings.LastIndex(function, " + 0x")
	if i <= 0 {
		return "", 0, false
	}
	off, err := strconv.ParseUint(function[i+5:], 16, 64)
	if err != nil {
		return "", 0, false
	}
	bin := function[:i]
	if pkg != "" && pkg != bin {
		return "", 0, false
	}
	return bin, off, true
}
