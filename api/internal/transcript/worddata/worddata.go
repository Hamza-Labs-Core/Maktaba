// Package worddata reads and writes a transcript sentence's packed words (Epic 52, story
// 52.1): transcript_segments.word_data.
//
// The layout is little-endian, a 3-byte header and 9 bytes a word (the spec said 10; its fields add up to 9):
//
//	version:u8 (=1) | n:u16
//	n × { char_start:u16, char_len:u8, start_cs:u16, dur_cs:u16, prob:u8, flags:u8 }
//
// char_start and char_len count Unicode code points of the sentence's text (Go indexes
// runes, never bytes); start_cs is centiseconds from the sentence's start. The Python twin
// is pipeline/src/maktaba_pipeline/stt/word_data.py; both read the golden fixtures in
// shared/testdata/word_data.
package worddata

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode"
)

// Version is the layout this package reads and writes.
const Version = 1

// Flag bits of a word.
const (
	HeardDifferently = 1 << 0 // the second listen's word replaced Whisper's
	Inserted         = 1 << 1 // the second listen heard a word Whisper missed
	Quran            = 1 << 2 // inside a run of the Quran
	NoProb           = 1 << 3 // the engine gave no probability
)

const (
	headSize = 3
	wordSize = 9
)

// Slot is one packed word.
type Slot struct {
	CharStart int `json:"char_start"`
	CharLen   int `json:"char_len"`
	StartCS   int `json:"start_cs"`
	DurCS     int `json:"dur_cs"`
	Prob      int `json:"prob"`
	Flags     int `json:"flags"`
}

// ErrSpan is an edit whose span is not inside the sentence.
var ErrSpan = errors.New("worddata: span outside the text")

// Decode reads data.
func Decode(data []byte) ([]Slot, error) {
	if len(data) < headSize {
		return nil, errors.New("worddata: too short")
	}
	if data[0] != Version {
		return nil, fmt.Errorf("worddata: version %d", data[0])
	}
	n := int(binary.LittleEndian.Uint16(data[1:3]))
	if len(data) != headSize+n*wordSize {
		return nil, errors.New("worddata: length does not match its count")
	}
	out := make([]Slot, n)
	for i := range out {
		b := data[headSize+i*wordSize:]
		out[i] = Slot{
			CharStart: int(binary.LittleEndian.Uint16(b[0:2])),
			CharLen:   int(b[2]),
			StartCS:   int(binary.LittleEndian.Uint16(b[3:5])),
			DurCS:     int(binary.LittleEndian.Uint16(b[5:7])),
			Prob:      int(b[7]),
			Flags:     int(b[8]),
		}
	}
	return out, nil
}

// Encode writes slots (each field already in range).
func Encode(slots []Slot) []byte {
	out := make([]byte, headSize+len(slots)*wordSize)
	out[0] = Version
	binary.LittleEndian.PutUint16(out[1:3], uint16(len(slots)))
	for i, s := range slots {
		b := out[headSize+i*wordSize:]
		binary.LittleEndian.PutUint16(b[0:2], uint16(s.CharStart))
		b[2] = byte(s.CharLen)
		binary.LittleEndian.PutUint16(b[3:5], uint16(s.StartCS))
		binary.LittleEndian.PutUint16(b[5:7], uint16(s.DurCS))
		b[7] = byte(s.Prob)
		b[8] = byte(s.Flags)
	}
	return out
}

// ShiftAfter moves every word at or after charStart by delta code points.
func ShiftAfter(data []byte, charStart, delta int) ([]byte, error) {
	slots, err := Decode(data)
	if err != nil {
		return nil, err
	}
	for i := range slots {
		if slots[i].CharStart >= charStart {
			slots[i].CharStart += delta
		}
	}
	return Encode(slots), nil
}

func touches(s Slot, a, b int) bool {
	if b > a {
		return s.CharStart < b && s.CharStart+s.CharLen > a
	}
	return s.CharStart < a && a < s.CharStart+s.CharLen
}

type run struct{ a, b int }

func runs(text []rune, a, b int) []run {
	var out []run
	for i := a; i < b; {
		if isSpace(text[i]) {
			i++
			continue
		}
		j := i
		for j < b && !isSpace(text[j]) {
			j++
		}
		out = append(out, run{i, j})
		i = j
	}
	return out
}

// isSpace matches Python's str.isspace closely enough for sentence text.
func isSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// pyRound is Python's round(): halves go to the even neighbour.
func pyRound(x float64) int { return int(math.RoundToEven(x)) }

// Edit returns the sentence's text and word data after its span [charStart,
// charStart+charLen) (code points) reads after. The words the span touches become the
// words of the new text over their union, sharing the first one's start to the last one's
// end evenly, checked by a person (prob 255, only the Quran mark kept); the words after it
// move by the length difference, their times kept. data may be nil (no word data).
func Edit(text string, data []byte, charStart, charLen int, after string) (string, []byte, error) {
	rs := []rune(text)
	if charStart < 0 || charLen < 0 || charStart+charLen > len(rs) {
		return "", nil, ErrSpan
	}
	ar := []rune(after)
	nt := make([]rune, 0, len(rs)-charLen+len(ar))
	nt = append(nt, rs[:charStart]...)
	nt = append(nt, ar...)
	nt = append(nt, rs[charStart+charLen:]...)
	newText := string(nt)
	if data == nil {
		return newText, nil, nil
	}
	slots, err := Decode(data)
	if err != nil {
		return "", nil, err
	}
	delta := len(ar) - charLen
	end := charStart + charLen
	var hit []int
	for i, s := range slots {
		if touches(s, charStart, end) {
			hit = append(hit, i)
		}
	}
	shifted := func(ss []Slot) []Slot {
		out := make([]Slot, len(ss))
		for i, s := range ss {
			s.CharStart += delta
			out[i] = s
		}
		return out
	}
	var out []Slot
	if len(hit) == 0 {
		var before, rest []Slot
		for _, s := range slots {
			if s.CharStart+s.CharLen <= charStart {
				before = append(before, s)
			} else {
				rest = append(rest, s)
			}
		}
		at := 0
		if len(before) > 0 {
			at = before[len(before)-1].StartCS + before[len(before)-1].DurCS
		}
		out = append(out, before...)
		for _, r := range runs(nt, charStart, charStart+len(ar)) {
			out = append(out, Slot{CharStart: r.a, CharLen: r.b - r.a, StartCS: at, Prob: 255})
		}
		out = append(out, shifted(rest)...)
		return newText, Encode(out), nil
	}
	first, last := slots[hit[0]], slots[hit[len(hit)-1]]
	uStart := min(charStart, first.CharStart)
	uEnd := max(end, last.CharStart+last.CharLen) + delta
	t0 := first.StartCS
	t1 := max(t0, last.StartCS+last.DurCS)
	quran := 0
	for _, i := range hit {
		quran |= slots[i].Flags & Quran
	}
	var keep []run
	for _, r := range runs(nt, uStart, uEnd) {
		if r.b-r.a <= 0xFF {
			keep = append(keep, r)
		}
	}
	out = append(out, slots[:hit[0]]...)
	if len(keep) > 0 {
		step := float64(t1-t0) / float64(len(keep))
		for k, r := range keep {
			s0 := t0 + pyRound(float64(k)*step)
			s1 := t0 + pyRound(float64(k+1)*step)
			out = append(out, Slot{CharStart: r.a, CharLen: r.b - r.a, StartCS: s0, DurCS: s1 - s0, Prob: 255, Flags: quran})
		}
	}
	out = append(out, shifted(slots[hit[len(hit)-1]+1:])...)
	return newText, Encode(out), nil
}
