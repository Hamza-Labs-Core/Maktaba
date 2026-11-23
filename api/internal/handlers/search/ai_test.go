package search

// Story 49.12, AI mode's API: the sentence splitter and the citation check, the stream
// (sources, text, done) with a model that cites, invents and refuses, the 409s, the line of
// one answer at a time, follow-ups, Ollama's API, and the role test on SQLite and Postgres:
// a limited account is never given, nor cited, a passage of another library.

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func TestSentenceSplitter_CutsAfterTheMarkers(t *testing.T) {
	stream := "الصبر مفتاح الفرج [1]. Patience is light [2][3]. The rate is 3.5 percent [1]! " +
		"لماذا؟ [2]\nA last one without an end [4]"
	want := []string{
		"الصبر مفتاح الفرج [1].",
		"Patience is light [2][3].",
		"The rate is 3.5 percent [1]!",
		"لماذا؟ [2]",
		"A last one without an end [4]",
	}
	// the same sentences whatever the pieces: one rune at a time, or all at once
	for _, step := range []int{1, 3, 1000} {
		s := &sentenceSplitter{}
		var got []string
		r := []rune(stream)
		for i := 0; i < len(r); i += step {
			end := min(i+step, len(r))
			got = append(got, s.add(string(r[i:end]))...)
		}
		if rest := s.flush(); rest != "" {
			got = append(got, rest)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d:\n got %q\nwant %q", step, got, want)
		}
	}
	// a marker after the end, still arriving, keeps the sentence open
	s := &sentenceSplitter{}
	if got := s.add("One. [1"); len(got) != 0 {
		t.Fatalf("cut before the marker ended: %q", got)
	}
	if got := s.add("] Two"); !reflect.DeepEqual(got, []string{"One. [1]"}) {
		t.Fatalf("got %q", got)
	}
}

func TestCiteSentence_KeepsTheFirstFive(t *testing.T) {
	srcs := make([]AISource, 8)
	for i := range srcs {
		srcs[i] = AISource{N: i + 1, VideoID: "v", Text: "t"}
	}
	ev, ok := citeSentence("Everything [8][7][6][5][4][3][2].", srcs)
	var ns []int
	for _, c := range ev.Citations {
		ns = append(ns, c.N)
	}
	if !ok || !reflect.DeepEqual(ns, []int{4, 5, 6, 7, 8}) {
		t.Fatalf("citations %v", ns)
	}
}

func TestCiteSentence(t *testing.T) {
	srcs := []AISource{
		{N: 1, VideoID: "v1", StartSec: 10, EndSec: 19, Text: "first"},
		{N: 2, VideoID: "v2", StartSec: 20, EndSec: 29, Text: "second"},
		{N: 3, VideoID: "v3", StartSec: 30, EndSec: 39, Text: "third"},
	}
	cases := []struct {
		in   string
		text string
		ns   []int
		ok   bool
	}{
		{"Patience is light [1].", "Patience is light.", []int{1}, true},
		{"Two sources [3][1].", "Two sources.", []int{1, 3}, true},
		{"A list [1, 2].", "A list.", []int{1, 2}, true},
		{"الصبر ضياء [٢].", "الصبر ضياء.", []int{2}, true}, // Arabic-Indic digits
		{"قال [١، ٣] ذلك.", "قال ذلك.", []int{1, 3}, true},
		{"Named [passage 2].", "Named.", []int{2}, true},
		{"A made-up source [9].", "", nil, false},
		{"Kept, the invented one left out [9][2].", "Kept, the invented one left out.", []int{2}, true},
		{"No citation at all.", "", nil, false},
		{"[1].", "", nil, false}, // a marker alone is no sentence
		{"Too many [3][2][1][3][2][1][1][2][3].", "Too many.", []int{1, 2, 3}, true},
	}
	for _, c := range cases {
		ev, ok := citeSentence(c.in, srcs)
		if ok != c.ok {
			t.Fatalf("%q: ok %v", c.in, ok)
		}
		if !ok {
			continue
		}
		var ns []int
		for _, cit := range ev.Citations {
			ns = append(ns, cit.N)
			src := srcs[cit.N-1]
			if cit.VideoID != src.VideoID || cit.StartSec != src.StartSec || cit.Quote != src.Text {
				t.Fatalf("%q: citation %+v is not passage %d", c.in, cit, cit.N)
			}
		}
		if ev.Text != c.text || !reflect.DeepEqual(ns, c.ns) {
			t.Fatalf("%q: %q %v, want %q %v", c.in, ev.Text, ns, c.text, c.ns)
		}
	}
}

// fakeModel answers with reply (or by calling answer), recording what it was asked.
type fakeModel struct {
	mu      sync.Mutex
	ready   error
	reply   string
	answer  func(ctx context.Context, msgs []AIMessage, piece func(string) error) error
	asked   [][]AIMessage
	servers []string
}

func (m *fakeModel) Ready(_ context.Context, server, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers = append(m.servers, server+" "+model)
	return m.ready
}

func (m *fakeModel) Chat(ctx context.Context, _, _ string, msgs []AIMessage, piece func(string) error) error {
	m.mu.Lock()
	m.asked = append(m.asked, msgs)
	answer, reply := m.answer, m.reply
	m.mu.Unlock()
	if answer != nil {
		return answer(ctx, msgs, piece)
	}
	// a few runes at a time, as a model streams
	r := []rune(reply)
	for i := 0; i < len(r); i += 4 {
		if err := piece(string(r[i:min(i+4, len(r))])); err != nil {
			return err
		}
	}
	return nil
}

func (m *fakeModel) prompts() [][]AIMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]AIMessage(nil), m.asked...)
}

type sseEvent struct {
	Event string
	Data  json.RawMessage
}

func readSSE(t *testing.T, body io.Reader) []sseEvent {
	t.Helper()
	var out []sseEvent
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var ev sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			ev.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.Data = json.RawMessage(strings.TrimPrefix(line, "data: "))
		case line == "" && ev.Event != "":
			out = append(out, ev)
			ev = sseEvent{}
		}
	}
	return out
}

func eventsOf(t *testing.T, rec *httptest.ResponseRecorder) []sseEvent {
	t.Helper()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d %q: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	return readSSE(t, rec.Body)
}

func dataOf[T any](t *testing.T, ev sseEvent) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(ev.Data, &v); err != nil {
		t.Fatalf("%s: %v: %s", ev.Event, err, ev.Data)
	}
	return v
}

// aiAnswer is a stream's sources, sentences and done.
type aiAnswer struct {
	names   []string
	sources AISourcesEvent
	texts   []AITextEvent
	done    AIDoneEvent
}

func answerOf(t *testing.T, evs []sseEvent) aiAnswer {
	t.Helper()
	var a aiAnswer
	for _, ev := range evs {
		a.names = append(a.names, ev.Event)
		switch ev.Event {
		case "sources":
			a.sources = dataOf[AISourcesEvent](t, ev)
		case "text":
			a.texts = append(a.texts, dataOf[AITextEvent](t, ev))
		case "done":
			a.done = dataOf[AIDoneEvent](t, ev)
		}
	}
	return a
}

// checkCited: every sentence cites at least one passage the stream listed, and quotes it.
func checkCited(t *testing.T, a aiAnswer) {
	t.Helper()
	by := map[int]AISource{}
	for _, s := range a.sources.Passages {
		by[s.N] = s
	}
	for _, tx := range a.texts {
		if len(tx.Citations) == 0 {
			t.Fatalf("a sentence with no citation: %+v", tx)
		}
		for _, c := range tx.Citations {
			s, ok := by[c.N]
			if !ok || s.VideoID != c.VideoID || s.StartSec != c.StartSec || s.Text != c.Quote {
				t.Fatalf("citation %+v is not a listed passage (%+v)", c, s)
			}
		}
	}
}

func aiLibrary(t *testing.T) *sql.DB {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "Lessons")
	f.library(libB, "Other")
	f.video("va", libA, "On patience", 600)
	f.transcript("ta", "va", true, "the opening words", "patience is light in the dark", "and so it was taught")
	f.video("vb", libA, "Gratitude", 600)
	f.transcript("tb", "vb", true, "patience and gratitude go together")
	f.video("vc", libB, "Secret talk", 600)
	f.transcript("tc", "vc", true, "patience secret of the other library")
	return db
}

func TestAI_StreamsSourcesThenCitedSentences(t *testing.T) {
	db := aiLibrary(t)
	model := &fakeModel{reply: "Patience is called light [1]. It goes with gratitude [2]. " +
		"An invented source [7]. A sentence with nothing behind it. It was taught so [1]."}
	h := router(&Handler{DB: db, AI: model})
	a := answerOf(t, eventsOf(t, call(t, h, admin, http.MethodPost, "/api/search/ai", `{"q":"patience"}`)))
	if a.names[0] != "sources" || a.names[len(a.names)-1] != "done" {
		t.Fatalf("events %v: sources first, done last", a.names)
	}
	if len(a.sources.Passages) < 2 {
		t.Fatalf("sources %+v", a.sources)
	}
	first := a.sources.Passages[0]
	if first.N != 1 || first.Text == "" || strings.Contains(first.Text, "<mark>") || first.Title == "" {
		t.Fatalf("a source is numbered, titled and plain: %+v", first)
	}
	checkCited(t, a)
	got := []string{}
	for _, tx := range a.texts {
		got = append(got, tx.Text)
	}
	want := []string{"Patience is called light.", "It goes with gratitude.", "It was taught so."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sentences %q, want %q", got, want)
	}
	if !a.done.Answered || a.done.Sentences != 3 || a.done.Dropped != 2 || a.done.Reason != "" {
		t.Fatalf("done %+v", a.done)
	}
	// the model read each passage with the lines around it, numbered as the sources are
	asked := model.prompts()
	if len(asked) != 1 {
		t.Fatalf("asked %d times", len(asked))
	}
	msgs := asked[0]
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, aiNoAnswer) {
		t.Fatalf("instructions %q", msgs[0].Content)
	}
	user := msgs[len(msgs)-1].Content
	if !strings.Contains(user, "the opening words patience is light in the dark and so it was taught") ||
		!strings.Contains(user, "Question: patience") || !strings.Contains(user, "[1] (") {
		t.Fatalf("prompt %q", user)
	}
}

func TestAI_SaysWhenThePassagesDoNotAnswer(t *testing.T) {
	db := aiLibrary(t)
	model := &fakeModel{reply: aiNoAnswer}
	h := router(&Handler{DB: db, AI: model})
	a := answerOf(t, eventsOf(t, call(t, h, admin, http.MethodPost, "/api/search/ai", `{"q":"patience"}`)))
	if a.done.Answered || a.done.Reason != "not-in-passages" || len(a.texts) != 0 || len(a.sources.Passages) == 0 {
		t.Fatalf("an unanswered question shows its passages and says so: %+v", a)
	}
	// a model that answers only without citations answers nothing either
	model.reply = "Patience is a virtue. Everyone knows it."
	a = answerOf(t, eventsOf(t, call(t, h, admin, http.MethodPost, "/api/search/ai", `{"q":"patience"}`)))
	if a.done.Answered || a.done.Reason != "not-in-passages" || a.done.Dropped != 2 {
		t.Fatalf("uncited sentences are no answer: %+v", a.done)
	}
	// nothing found: no model asked at all
	before := len(model.prompts())
	a = answerOf(t, eventsOf(t, call(t, h, admin, http.MethodPost, "/api/search/ai", `{"q":"zakat"}`)))
	if a.done.Reason != "no-passages" || len(a.sources.Passages) != 0 || len(model.prompts()) != before {
		t.Fatalf("no passages: %+v", a)
	}
}

func TestAI_Refusals(t *testing.T) {
	db := aiLibrary(t)
	model := &fakeModel{reply: "x [1]."}
	h := &Handler{DB: db, AI: model}
	r := router(h)
	rec := call(t, r, admin, http.MethodPost, "/api/search/ai", `{"q":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty q: %d", rec.Code)
	}
	type problem struct {
		Code     string         `json:"code"`
		Help     map[string]any `json:"help"`
		Position int            `json:"position"`
	}
	// no model on the server: 409 with where to set one up
	model.ready = errAIUnavailable
	p := decode[problem](t, call(t, r, admin, http.MethodPost, "/api/search/ai", `{"q":"patience"}`), http.StatusConflict)
	if p.Code != "ai-unavailable" || p.Help["settings"] == nil || p.Help["help"] == nil {
		t.Fatalf("no model: %+v", p)
	}
	model.ready = nil
	// the llm component switched off
	h.LLMOn = func() bool { return false }
	p = decode[problem](t, call(t, r, admin, http.MethodPost, "/api/search/ai", `{"q":"patience"}`), http.StatusConflict)
	if p.Code != "ai-unavailable" {
		t.Fatalf("llm off: %+v", p)
	}
	h.LLMOn = nil
	// an admin turned AI mode off
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT, updated_by TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.search', '{"ai_mode": false}')`); err != nil {
		t.Fatal(err)
	}
	p = decode[problem](t, call(t, r, admin, http.MethodPost, "/api/search/ai", `{"q":"patience"}`), http.StatusConflict)
	if p.Code != "ai-off" {
		t.Fatalf("off: %+v", p)
	}
	if _, err := db.Exec(`UPDATE app_settings SET value = '{"ai_model": "llama3:8b"}'`); err != nil {
		t.Fatal(err)
	}
	// the line full: 429 with the place it would have had
	for range aiMaxWaiting + 1 {
		if _, _, ok := h.ai.join(); !ok {
			t.Fatal("the line filled early")
		}
	}
	rec = call(t, r, admin, http.MethodPost, "/api/search/ai", `{"q":"patience"}`)
	p = decode[problem](t, rec, http.StatusTooManyRequests)
	if p.Code != "ai-busy" || p.Position != aiMaxWaiting+1 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("busy: %+v", p)
	}
	// rules.search.ai_model names the model; the server is the machine's own
	h.Env = func(k string) string {
		if k == "MAKTABA_OLLAMA_URL" {
			return "http://127.0.0.1:11533/"
		}
		return ""
	}
	if server, m := h.aiServer(context.Background(), h.rules(context.Background())); server != "http://127.0.0.1:11533" || m != "llama3:8b" {
		t.Fatalf("server %q, model %q", server, m)
	}
}

func TestAI_FollowUpCarriesTheEarlierTurns(t *testing.T) {
	db := aiLibrary(t)
	model := &fakeModel{reply: "Together with gratitude [1]."}
	h := router(&Handler{DB: db, AI: model})
	history := `[{"q":"q1","answer":"a1"},{"q":"q2","answer":"a2"},{"q":"q3","answer":"a3"},{"q":"q4","answer":"a4"},{"q":"","answer":"skipped"}]`
	a := answerOf(t, eventsOf(t, call(t, h, admin, http.MethodPost, "/api/search/ai",
		`{"q":"gratitude","history":`+history+`}`)))
	if !a.done.Answered || len(a.sources.Passages) != 1 || a.sources.Passages[0].VideoID != "vb" {
		t.Fatalf("retrieval runs on the new question: %+v", a.sources)
	}
	msgs := model.prompts()[0]
	var roles, contents []string
	for _, m := range msgs[1 : len(msgs)-1] {
		roles = append(roles, m.Role)
		contents = append(contents, m.Content)
	}
	// the last three turns that have both a question and an answer
	if !reflect.DeepEqual(contents, []string{"q2", "a2", "q3", "a3", "q4", "a4"}) ||
		!reflect.DeepEqual(roles, []string{"user", "assistant", "user", "assistant", "user", "assistant"}) {
		t.Fatalf("history %v %v", roles, contents)
	}
}

// One answer at a time: a second question waits, told its place, and is answered when the
// first is done; a question that leaves the line gives its place up.
func TestAI_OneAnswerAtATime(t *testing.T) {
	db := aiLibrary(t)
	release := make(chan struct{})
	model := &fakeModel{answer: func(ctx context.Context, _ []AIMessage, piece func(string) error) error {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return piece("Patience is light [1].")
	}}
	hh := &Handler{DB: db, AI: model}
	mux := router(hh)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), admin)))
	}))
	defer srv.Close()
	ask := func() (*http.Response, *bufio.Reader) {
		res, err := http.Post(srv.URL+"/api/search/ai", "application/json", strings.NewReader(`{"q":"patience"}`))
		if err != nil {
			t.Fatal(err)
		}
		return res, bufio.NewReader(res.Body)
	}
	next := func(r *bufio.Reader) sseEvent {
		var ev sseEvent
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("stream ended: %v", err)
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.Data = json.RawMessage(strings.TrimPrefix(line, "data: "))
			case line == "" && ev.Event != "":
				return ev
			}
		}
	}
	first, r1 := ask()
	defer first.Body.Close()
	if ev := next(r1); ev.Event != "sources" {
		t.Fatalf("first: %s", ev.Event)
	}
	second, r2 := ask()
	defer second.Body.Close()
	if ev := next(r2); ev.Event != "queue" || dataOf[AIQueueEvent](t, ev).Position != 1 {
		t.Fatalf("second: %s %s", ev.Event, ev.Data)
	}
	third, r3 := ask()
	if ev := next(r3); ev.Event != "queue" || dataOf[AIQueueEvent](t, ev).Position != 2 {
		t.Fatalf("third: %s %s", ev.Event, ev.Data)
	}
	// the third leaves: the line is the first answering and the second waiting
	third.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		hh.ai.mu.Lock()
		n := len(hh.ai.waiting)
		hh.ai.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d waiting after the third left", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(model.prompts()) != 1 {
		t.Fatalf("the model was asked %d times while the first answered", len(model.prompts()))
	}
	close(release)
	for _, r := range []*bufio.Reader{r1, r2} {
		var names []string
		for ev := next(r); ; ev = next(r) {
			names = append(names, ev.Event)
			if ev.Event == "done" {
				if !dataOf[AIDoneEvent](t, ev).Answered {
					t.Fatalf("not answered: %s", ev.Data)
				}
				break
			}
		}
		if names[len(names)-2] != "text" {
			t.Fatalf("events %v", names)
		}
	}
	if len(model.prompts()) != 2 {
		t.Fatalf("asked %d times", len(model.prompts()))
	}
}

func TestAILine_Places(t *testing.T) {
	var l aiLine
	a, n, _ := l.join()
	b, nb, _ := l.join()
	c, nc, _ := l.join()
	if n != 0 || nb != 1 || nc != 2 {
		t.Fatalf("places %d %d %d", n, nb, nc)
	}
	<-b.pos
	<-c.pos
	l.leave(b) // the second gives up its place: the third is next
	if p := <-c.pos; p != 1 {
		t.Fatalf("third now %d", p)
	}
	l.leave(a)
	select {
	case <-c.turn:
	default:
		t.Fatal("the turn did not pass")
	}
	l.leave(c)
	if d, n, _ := l.join(); n != 0 {
		t.Fatalf("an empty line answers at once: %d", n)
	} else {
		l.leave(d)
	}
}

// the role test: a limited account is given, and shown, only its libraries' passages
func aiRoleCase(t *testing.T, db *sql.DB, va, vc string) {
	t.Helper()
	model := &fakeModel{reply: "Patience is light [1]. The secret [2]. And more [3]."}
	h := router(&Handler{DB: db, AI: model})
	limited := &principal.Principal{UserID: userA, Libraries: []string{libA}}
	for _, body := range []string{
		`{"q":"patience secret"}`,
		// a library asked for that the account may not see is dropped
		`{"q":"patience secret","filters":{"library_id":["` + libB + `"]}}`,
	} {
		a := answerOf(t, eventsOf(t, call(t, h, limited, http.MethodPost, "/api/search/ai", body)))
		if strings.Contains(body, "filters") {
			if len(a.sources.Passages) != 0 || a.done.Reason != "no-passages" {
				t.Fatalf("only another library asked for: %+v", a)
			}
		} else if len(a.sources.Passages) == 0 || !a.done.Answered {
			t.Fatalf("the account's own library answers: %+v", a)
		}
		for _, s := range a.sources.Passages {
			if canonID(s.VideoID) != canonID(va) || s.LibraryID != libA {
				t.Fatalf("%s: a passage of another library: %+v", body, s)
			}
		}
		checkCited(t, a)
		for _, tx := range a.texts {
			for _, c := range tx.Citations {
				if canonID(c.VideoID) == canonID(vc) {
					t.Fatalf("cited another library's video: %+v", c)
				}
			}
		}
	}
	for _, msgs := range model.prompts() {
		for _, m := range msgs {
			if strings.Contains(m.Content, "other library") {
				t.Fatalf("the model was given another library's words: %q", m.Content)
			}
		}
	}
	// the admin is given both
	a := answerOf(t, eventsOf(t, call(t, h, admin, http.MethodPost, "/api/search/ai", `{"q":"patience secret"}`)))
	libs := map[string]bool{}
	for _, s := range a.sources.Passages {
		libs[s.LibraryID] = true
	}
	if !libs[libA] || !libs[libB] {
		t.Fatalf("admin sources %+v", a.sources.Passages)
	}
}

func TestAI_LimitedAccountRole_SQLite(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "Lessons")
	f.library(libB, "Other")
	f.video("va", libA, "On patience", 600)
	f.transcript("ta", "va", true, "patience is light", "patience secret words of lessons")
	f.video("vc", libB, "Secret", 600)
	f.transcript("tc", "vc", true, "patience secret of the other library")
	aiRoleCase(t, db, "va", "vc")
}

func TestAI_LimitedAccountRole_Postgres(t *testing.T) {
	db := pgSearchDB(t)
	va, vc := pgUUID(1), pgUUID(2)
	tr := pgVideo(t, db, va, libA, "On patience")
	pgTranscript(t, db, pgUUID(11), va, tr, true, "patience is light", "patience secret words of lessons")
	tr = pgVideo(t, db, vc, libB, "Secret")
	pgTranscript(t, db, pgUUID(12), vc, tr, true, "patience secret of the other library")
	aiRoleCase(t, db, va, vc)
}

func TestOllama_ReadyAndChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"qwen2.5:7b","model":"qwen2.5:7b"},{"name":"llama3:latest","model":"llama3:latest"}]}`)
		case "/api/chat":
			var body struct {
				Model    string      `json:"model"`
				Stream   bool        `json:"stream"`
				Messages []AIMessage `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Model == "broken" {
				_, _ = io.WriteString(w, `{"error":"model failed to load"}`+"\n")
				return
			}
			if !body.Stream || len(body.Messages) != 2 {
				http.Error(w, "bad", 400)
				return
			}
			for _, p := range []string{"Pat", "ience [1]", "."} {
				_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"`+p+`"},"done":false}`+"\n")
			}
			_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":""},"done":true}`+"\n")
		}
	}))
	defer srv.Close()
	o := ollama{client: srv.Client()}
	ctx := context.Background()
	if err := o.Ready(ctx, srv.URL, "qwen2.5:7b"); err != nil {
		t.Fatal(err)
	}
	if err := o.Ready(ctx, srv.URL, "llama3"); err != nil {
		t.Fatalf("a model named without its tag is :latest: %v", err)
	}
	if err := o.Ready(ctx, srv.URL, "mistral"); err == nil {
		t.Fatal("a model the server lacks is ready")
	}
	if err := o.Ready(ctx, "http://127.0.0.1:1", "x"); err == nil {
		t.Fatal("no server is ready")
	}
	var got strings.Builder
	msgs := []AIMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}}
	if err := o.Chat(ctx, srv.URL, "qwen2.5:7b", msgs, func(p string) error { got.WriteString(p); return nil }); err != nil {
		t.Fatal(err)
	}
	if got.String() != "Patience [1]." {
		t.Fatalf("got %q", got.String())
	}
	if err := o.Chat(ctx, srv.URL, "broken", msgs, func(string) error { return nil }); err == nil || !strings.Contains(err.Error(), "failed to load") {
		t.Fatalf("an error line: %v", err)
	}
}
