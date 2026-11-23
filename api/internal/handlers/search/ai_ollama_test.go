package search

// Story 49.12 with a real language model: AI mode on a small library of Arabic and English
// lessons, asked through Ollama's API. It runs only with MAKTABA_TEST_OLLAMA_URL set (the
// model in MAKTABA_TEST_OLLAMA_MODEL, default qwen2.5:7b), and logs each answer's first-token
// time: how 49.12's first-token time was measured on a home's model server.
//
//	MAKTABA_TEST_OLLAMA_URL=http://127.0.0.1:11533 go test ./internal/handlers/search/ -run TestAI_RealModel -v

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestAI_RealModel(t *testing.T) {
	url := os.Getenv("MAKTABA_TEST_OLLAMA_URL")
	if url == "" {
		t.Skip("MAKTABA_TEST_OLLAMA_URL unset: no model server to ask")
	}
	model := os.Getenv("MAKTABA_TEST_OLLAMA_MODEL")
	if model == "" {
		model = "qwen2.5:7b"
	}
	db := searchDB(t)
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT, updated_by TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.tagcheck', $1), ('rules.search', $2)`,
		`{"url": "`+url+`"}`, `{"ai_model": "`+model+`"}`); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: db}
	f.library(libA, "دروس")
	f.video("v1", libA, "الصبر عند المصيبة", 1800)
	f.transcript("t1", "v1", true,
		"بسم الله والحمد لله",
		"الصبر عند المصيبة أن يحبس المرء نفسه عن الجزع ولسانه عن الشكوى",
		"وقد قال النبي صلى الله عليه وسلم إنما الصبر عند الصدمة الأولى",
		"فمن صبر في أول المصيبة كان له الأجر كاملا",
		"والصبر ثلاثة أنواع صبر على الطاعة وصبر عن المعصية وصبر على الأقدار",
	)
	f.video("v2", libA, "شكر النعمة", 1500)
	f.transcript("t2", "v2", true,
		"الشكر يكون بالقلب واللسان والجوارح",
		"والصبر والشكر قرينان فالمؤمن بين صبر على البلاء وشكر على النعماء",
		"ومن شكر النعمة أن يستعملها في طاعة الله",
	)
	f.video("v3", libA, "Patience in hardship", 1200)
	f.transcript("t3", "v3", true,
		"Patience at the first blow is the patience that counts.",
		"The scholars name three kinds of patience: in obedience, away from sin, and with what is decreed.",
		"Gratitude and patience go together in the life of a believer.",
	)
	// a fuller library: twelve more lessons that each say الصبر in ten lines, so a question
	// about it fills the prompt (30 passages with the lines around them)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("w%d", i)
		f.video(id, libA, fmt.Sprintf("درس في الأخلاق %d", i+1), 2400)
		var lines []string
		for j := 0; j < 10; j++ {
			lines = append(lines, fmt.Sprintf("ومن الأخلاق التي ذكرها الشيخ في الدرس %d المقطع %d الصبر على أذى الناس وحسن الظن بهم وترك الغضب عند الخصومة", i+1, j+1))
		}
		f.transcript("tw"+id, id, true, lines...)
	}
	h := router(&Handler{DB: db})
	for _, c := range []struct {
		q      string
		answer bool
	}{
		{"ما هي أنواع الصبر؟", true},
		{"What are the kinds of patience?", true},
		{"متى يكون الصبر؟", true},
		{"What is the boiling point of water at sea level?", false},
		// the passages speak of the hadith but name no narrator
		{"Who narrated the hadith that patience is at the first blow?", false},
		// the lessons name patience with people's harm among good manners; none says its virtue
		{"ما فضل الصبر على أذى الناس؟", false},
	} {
		rec := call(t, h, admin, http.MethodPost, "/api/search/ai", `{"q":"`+c.q+`"}`)
		a := answerOf(t, eventsOf(t, rec))
		checkCited(t, a)
		var text []string
		for _, tx := range a.texts {
			var ns []string
			for _, cit := range tx.Citations {
				ns = append(ns, cit.VideoID)
			}
			text = append(text, tx.Text+" ("+strings.Join(ns, ",")+")")
		}
		t.Logf("%q: %d passages, answered %v (%s), %d sentences, %d dropped, first token %d ms, %d ms in all\n  %s",
			c.q, len(a.sources.Passages), a.done.Answered, a.done.Reason, a.done.Sentences, a.done.Dropped,
			a.done.FirstTokenMs, a.done.TookMs, strings.Join(text, "\n  "))
		if c.answer && !a.done.Answered {
			t.Errorf("%q: no answer from passages that hold one", c.q)
		}
		if !c.answer && a.done.Answered {
			t.Errorf("%q: answered from passages that do not answer it", c.q)
		}
	}
}
