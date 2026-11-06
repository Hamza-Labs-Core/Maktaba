package worddata

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fixtures = "../../../../shared/testdata/word_data"

type golden struct {
	Text  string `json:"text"`
	Words []struct {
		Text string `json:"text"`
	} `json:"words"`
	Slots []Slot `json:"slots"`
}

// The golden fixtures Python packed decode to the same code-point spans here (an emoji,
// harakat, an inserted word, a Quran run, an empty sentence), and encode back byte for byte.
func TestGoldenFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixtures, "*.json"))
	if err != nil || len(files) < 6 {
		t.Fatalf("fixtures: %v %d", err, len(files))
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var g golden
		if err := json.Unmarshal(raw, &g); err != nil {
			t.Fatal(err)
		}
		bin, err := os.ReadFile(strings.TrimSuffix(f, ".json") + ".bin")
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(bin)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(got) == 0 && len(g.Slots) == 0 {
			got = nil
			g.Slots = nil
		}
		if !reflect.DeepEqual(got, g.Slots) {
			t.Fatalf("%s: decoded %+v, want %+v", f, got, g.Slots)
		}
		if !bytes.Equal(Encode(got), bin) {
			t.Fatalf("%s: encode differs", f)
		}
		rs := []rune(g.Text)
		for i, s := range got {
			if word := string(rs[s.CharStart : s.CharStart+s.CharLen]); word != strings.TrimSpace(g.Words[i].Text) {
				t.Fatalf("%s: word %d reads %q, want %q", f, i, word, g.Words[i].Text)
			}
		}
	}
}

func TestDecodeRefusesBadData(t *testing.T) {
	for _, b := range [][]byte{nil, {1}, {2, 0, 0}, {1, 1, 0}} {
		if _, err := Decode(b); err == nil {
			t.Fatalf("%v decoded", b)
		}
	}
}

func TestShiftAfter(t *testing.T) {
	data := Encode([]Slot{{CharStart: 0, CharLen: 3}, {CharStart: 4, CharLen: 2}, {CharStart: 7, CharLen: 5}})
	got, err := ShiftAfter(data, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	slots, _ := Decode(got)
	if slots[0].CharStart != 0 || slots[1].CharStart != 7 || slots[2].CharStart != 10 {
		t.Fatalf("shifted %+v", slots)
	}
}

type editCase struct {
	Text      string `json:"text"`
	WordData  string `json:"word_data"`
	CharStart int    `json:"char_start"`
	CharLen   int    `json:"char_len"`
	After     string `json:"after"`
	WantText  string `json:"want_text"`
	WantData  string `json:"want_word_data"`
}

// Every golden edit case (shared with the Python writer) gives the same text and
// byte-identical word data.
func TestEditGoldenCases(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixtures, "edits", "*.json"))
	if err != nil || len(files) < 8 {
		t.Fatalf("edit fixtures: %v %d", err, len(files))
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var c editCase
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		data, _ := base64.StdEncoding.DecodeString(c.WordData)
		want, _ := base64.StdEncoding.DecodeString(c.WantData)
		text, got, err := Edit(c.Text, data, c.CharStart, c.CharLen, c.After)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if text != c.WantText {
			t.Fatalf("%s: text %q, want %q", f, text, c.WantText)
		}
		if !bytes.Equal(got, want) {
			gs, _ := Decode(got)
			ws, _ := Decode(want)
			t.Fatalf("%s: word data %+v, want %+v", f, gs, ws)
		}
	}
}

func TestEditRefusesASpanOutsideTheText(t *testing.T) {
	if _, _, err := Edit("abc", nil, 2, 5, "x"); err != ErrSpan {
		t.Fatalf("err %v", err)
	}
	text, data, err := Edit("ab😀cd", nil, 3, 2, "XY")
	if err != nil || text != "ab😀XY" || data != nil {
		t.Fatalf("%q %v %v", text, data, err)
	}
}
