package searchterms

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The pipeline's search/terms.py reads the same cases (test_python_keys_words_as_go_does).
func TestDocTermsMatchesThePipeline(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "testdata", "searchterms.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Text        string            `json:"text"`
		Terms       map[string]string `json:"terms"`
		SpeakerKeys [][2]string       `json:"speaker_keys"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := DocTerms(c.Text); !reflect.DeepEqual(got, c.Terms) {
			t.Errorf("%q:\n got %v\nwant %v", c.Text, got, c.Terms)
		}
		if c.SpeakerKeys != nil {
			if got := SpeakerKeys(strings.ReplaceAll(c.Text, "\t", "\n")); !reflect.DeepEqual(got, c.SpeakerKeys) {
				t.Errorf("speakers %q:\n got %v\nwant %v", c.Text, got, c.SpeakerKeys)
			}
		}
	}
}

func TestQueryKeys(t *testing.T) {
	if got := QueryKeys("الصبرُ عند المصيبة الصبر Café x"); !reflect.DeepEqual(got, []string{"الصبر", "عند", "المصيبه", "cafe"}) {
		t.Fatalf("%q", got)
	}
}

// Similarity is pg_trgm's: the values Postgres gives on ASCII (its trgm column).
func TestSimilarityIsPgTrgms(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"word", "words", 0.5714286}, // SELECT similarity('word', 'words')
		{"hello", "hallo", 0.33333334},
		{"abc", "abc", 1},
		{"abc", "xyz", 0},
		{"الصبر", "الصبير", 0.44444445}, // the trgm column's value of the pair
	} {
		if got := Similarity(c.a, c.b); math.Abs(got-c.want) > 1e-6 {
			t.Errorf("%s/%s: %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if !Arabic("مدرسه") || Arabic("cafe") {
		t.Fatal("Arabic")
	}
}
