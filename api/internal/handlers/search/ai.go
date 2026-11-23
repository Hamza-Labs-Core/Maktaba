package search

// AI mode's API (Epic 49, story 49.12): POST /api/search/ai answers a question from the
// library's own passages. The same hybrid search finds the best passages the caller may see;
// the home's language model reads them, with the lines around each, and writes a short
// answer. Every sentence must cite a passage it was given: a sentence that cites none, or
// only a passage that does not exist, is dropped. The answer streams as Server-Sent Events:
// `sources` first, then each checked sentence as a `text` event, then `done`. One answer is
// written at a time per home; the others wait in line, told their place.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	// aiVideos is how many of the search's best videos AI mode reads passages from
	aiVideos = 10
	// aiPerVideo is the most passages it takes from one video
	aiPerVideo = 3
	// aiWindowRunes is the most of one passage, with its lines around it, the model reads
	aiWindowRunes = 500
	// aiPromptRunes is the most of all the passages together
	aiPromptRunes = 14000
	// aiMaxQuestion is the longest question (runes); a longer one is cut
	aiMaxQuestion = 500
	// aiMaxTurns is how many earlier questions and answers a follow-up carries
	aiMaxTurns = 3
	// aiMaxAnswerRunes is the longest earlier answer a follow-up carries
	aiMaxAnswerRunes = 2000
	// aiMaxCitations is the most passages one sentence cites (the first it names)
	aiMaxCitations = 5
	// aiMaxSentences ends an answer that runs on
	aiMaxSentences = 12
	// aiMaxWaiting is the most questions waiting in line; one more is answered 429
	aiMaxWaiting = 8
	// aiHeartbeat keeps a stream open while it waits or the model thinks
	aiHeartbeat = 15 * time.Second
	// aiNoAnswer is what the model is told to reply when the passages do not answer
	aiNoAnswer = "NO_ANSWER"
)

// AIRequest is POST /api/search/ai's body.
type AIRequest struct {
	Q string `json:"q"`
	// History is the earlier questions and answers of this conversation, oldest first: the
	// last aiMaxTurns are given to the model; retrieval runs on Q alone.
	History []AITurn `json:"history,omitempty"`
	// Filters narrow the passages as a search's do; the libraries are the caller's.
	Filters Filters `json:"filters"`
}

// AITurn is one earlier question and its answer.
type AITurn struct {
	Q      string `json:"q"`
	Answer string `json:"answer"`
}

// AISource is one passage the model reads: the `sources` event lists them, numbered.
type AISource struct {
	N           int     `json:"n"`
	VideoID     string  `json:"video_id"`
	Title       string  `json:"title"`
	LibraryID   string  `json:"library_id"`
	LibraryName string  `json:"library_name"`
	PosterURL   string  `json:"poster_url"`
	SegmentID   int64   `json:"segment_id"`
	StartSec    float64 `json:"start_sec"`
	EndSec      float64 `json:"end_sec"`
	// Text is the passage's own words: what a citation quotes.
	Text string `json:"text"`

	// context: the passage with the lines around it, as the model reads it
	context string
}

// AISourcesEvent is the `sources` event.
type AISourcesEvent struct {
	Q        string     `json:"q"`
	Passages []AISource `json:"passages"`
}

// AICitation is where a sentence comes from: the video, the moment and the quoted words.
type AICitation struct {
	N        int     `json:"n"`
	VideoID  string  `json:"video_id"`
	StartSec float64 `json:"start_sec"`
	EndSec   float64 `json:"end_sec"`
	Quote    string  `json:"quote"`
}

// AITextEvent is one `text` event: a sentence of the answer with its citations.
type AITextEvent struct {
	Text      string       `json:"text"`
	Citations []AICitation `json:"citations"`
}

// AIQueueEvent is a `queue` event: the question's place in line (1: next).
type AIQueueEvent struct {
	Position int `json:"position"`
}

// AIDoneEvent is the last event.
type AIDoneEvent struct {
	// Answered is false when the passages do not answer the question: the sources are
	// what the client shows then.
	Answered bool `json:"answered"`
	// Reason says why there is no answer: "no-passages" (the search found nothing the
	// caller may see), "not-in-passages" (the model found no answer in them) or
	// "timeout" (the answer took too long; the sentences sent so far stand).
	Reason    string `json:"reason,omitempty"`
	Sentences int    `json:"sentences"`
	// Dropped counts the sentences left out for want of a citation to a given passage.
	Dropped      int    `json:"dropped"`
	FirstTokenMs int64  `json:"first_token_ms,omitempty"`
	TookMs       int64  `json:"took_ms"`
	Model        string `json:"model,omitempty"`
}

// AIErrorEvent is an `error` event: the answer stopped; the sentences sent stand.
type AIErrorEvent struct {
	Code string `json:"code"`
}

// AIMessage is one chat message to the model.
type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AIModel is the language model AI mode asks (nil Handler.AI: Ollama's API at the tag
// check's model server).
type AIModel interface {
	// Ready answers nil when server answers and has model.
	Ready(ctx context.Context, server, model string) error
	// Chat streams the reply to msgs, piece by piece, until it ends or piece fails.
	Chat(ctx context.Context, server, model string, msgs []AIMessage, piece func(string) error) error
}

// errAIUnavailable is Ready's answer when there is no model to ask.
var errAIUnavailable = errors.New("ai mode: no language model")

// aiHelp is where the 409 sends an admin: Settings' model server and the help page.
var aiHelp = map[string]any{
	"settings": "/settings/rules?section=tagcheck&q=url",
	"help":     "/help/search#ai-mode",
}

// aiServer is the model server and model AI mode asks: the tag check's server (the machine's
// own, MAKTABA_OLLAMA_URL, where the tag check keeps the default address), and rules.search's
// ai_model or the tag check's model.
func (h *Handler) aiServer(ctx context.Context, rl Rules) (server, model string) {
	server, model = "http://localhost:11434", "qwen2.5:7b"
	if h.DB != nil {
		if m, err := rules.Effective(ctx, h.DB, "tagcheck"); err == nil {
			if v, ok := m["url"].(string); ok && strings.TrimSpace(v) != "" {
				server = strings.TrimSpace(v)
			}
			if v, ok := m["model"].(string); ok && strings.TrimSpace(v) != "" {
				model = strings.TrimSpace(v)
			}
		}
	}
	env := os.Getenv
	if h.Env != nil {
		env = h.Env
	}
	if v := strings.TrimRight(strings.TrimSpace(env("MAKTABA_OLLAMA_URL")), "/"); v != "" && server == "http://localhost:11434" {
		server = v
	}
	if rl.AIModel != "" {
		model = rl.AIModel
	}
	return strings.TrimRight(server, "/"), model
}

func (h *Handler) aiModel() AIModel {
	if h.AI != nil {
		return h.AI
	}
	return defaultOllama
}

func aiRefuse(w http.ResponseWriter, r *http.Request, status int, typ, title, detail string, extras map[string]any) {
	httperror.Write(w, r, &httperror.Error{
		Type: httperror.Type(typ), Title: title, Status: status, Detail: detail, Extras: extras,
	})
}

// Answer is POST /api/search/ai.
func (h *Handler) Answer(w http.ResponseWriter, r *http.Request) {
	var req AIRequest
	if e := common.ReadJSON(r, &req, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Q = clipRunes(strings.TrimSpace(req.Q), aiMaxQuestion)
	if req.Q == "" {
		aiRefuse(w, r, http.StatusBadRequest, "invalid-query", "invalid query", "q must be non-empty", nil)
		return
	}
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	ctx := r.Context()
	rl := h.rules(ctx)
	if !rl.AIMode {
		aiRefuse(w, r, http.StatusConflict, "ai-off", "AI mode is off",
			"an admin turned AI mode off for this home", map[string]any{"code": "ai-off"})
		return
	}
	server, model := h.aiServer(ctx, rl)
	if h.LLMOn != nil && !h.LLMOn() {
		aiRefuse(w, r, http.StatusConflict, "ai-unavailable", "no language model",
			"the language model is switched off on this home", map[string]any{"code": "ai-unavailable", "help": aiHelp})
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := h.aiModel().Ready(rctx, server, model)
	cancel()
	if err != nil {
		h.log().InfoContext(ctx, "search ai: no model", "event", "search_ai_unavailable", "model", model, "err", err)
		aiRefuse(w, r, http.StatusConflict, "ai-unavailable", "no language model",
			"this home has no language model to answer with", map[string]any{"code": "ai-unavailable", "help": aiHelp})
		return
	}
	ticket, position, ok := h.ai.join()
	if !ok {
		w.Header().Set("Retry-After", "30")
		aiRefuse(w, r, http.StatusTooManyRequests, "ai-busy", "AI mode is busy",
			"other questions are waiting for their answers", map[string]any{"code": "ai-busy", "position": position})
		return
	}
	defer h.ai.leave(ticket)

	flusher, ok := w.(http.Flusher)
	if !ok {
		httperror.Write(w, r, httperror.Internal("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	// the stream is written from this goroutine and from the beat while the model reads
	var mu sync.Mutex
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}
	ping := func() {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(w, ": ping\n\n")
		flusher.Flush()
	}

	// in line: told the place until it is this question's turn
	beat := time.NewTicker(aiHeartbeat)
	defer beat.Stop()
	for waiting := position > 0; waiting; {
		select {
		case <-ticket.turn:
			waiting = false
		case n := <-ticket.pos:
			send("queue", AIQueueEvent{Position: n})
		case <-beat.C:
			ping()
		case <-ctx.Done():
			return
		}
	}

	t0 := time.Now()
	sources, err := h.aiSources(ctx, p, req, rl)
	if err != nil {
		h.log().WarnContext(ctx, "search ai: retrieval failed", "event", "search_ai_failed", "err", err)
		send("error", AIErrorEvent{Code: "retrieval-failed"})
		return
	}
	send("sources", AISourcesEvent{Q: req.Q, Passages: sources})
	if len(sources) == 0 {
		send("done", AIDoneEvent{Reason: "no-passages", TookMs: time.Since(t0).Milliseconds()})
		return
	}

	actx, stop := context.WithTimeout(ctx, time.Duration(rl.AITimeoutS)*time.Second)
	defer stop()
	done := AIDoneEvent{Model: model}
	split := &sentenceSplitter{}
	var firstToken time.Time
	var started atomic.Bool
	errEnough := errors.New("enough")
	emit := func(sentence string) error {
		if strings.Contains(sentence, aiNoAnswer) {
			return errEnough
		}
		ev, ok := citeSentence(sentence, sources)
		switch {
		case ok:
			send("text", ev)
			done.Sentences++
		case strings.TrimSpace(sentence) != "":
			done.Dropped++
		}
		if done.Sentences >= aiMaxSentences {
			return errEnough
		}
		return nil
	}
	// a beat while the model reads the passages (a long prompt can take a while)
	quiet, quietDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(quietDone)
		for {
			select {
			case <-quiet:
				return
			case <-beat.C:
				if !started.Load() {
					ping()
				}
			}
		}
	}()

	err = h.aiModel().Chat(actx, server, model, aiMessages(req, sources), func(piece string) error {
		if !started.Load() {
			firstToken = time.Now()
			started.Store(true)
		}
		for _, s := range split.add(piece) {
			if err := emit(s); err != nil {
				return err
			}
		}
		return nil
	})
	close(quiet)
	<-quietDone
	// the sentence still held (the last one waits for markers that may follow) is checked
	// whenever the stream ends, a timeout or a failed model included: only a cited one is
	// sent. Not when the caller left, nor after NO_ANSWER or the most sentences.
	if !errors.Is(err, errEnough) && ctx.Err() == nil {
		if rest := split.flush(); rest != "" {
			if e := emit(rest); err == nil {
				err = e
			}
		}
	}
	if !firstToken.IsZero() {
		done.FirstTokenMs = firstToken.Sub(t0).Milliseconds()
	}
	done.TookMs = time.Since(t0).Milliseconds()
	done.Answered = done.Sentences > 0
	switch {
	case err == nil || errors.Is(err, errEnough):
	case ctx.Err() != nil:
		return // the caller left
	case actx.Err() != nil:
		done.Reason = "timeout"
	default:
		h.log().WarnContext(ctx, "search ai: the model failed", "event", "search_ai_failed", "model", model, "err", err)
		send("error", AIErrorEvent{Code: "model-failed"})
		return
	}
	if !done.Answered && done.Reason == "" {
		done.Reason = "not-in-passages"
	}
	h.log().InfoContext(ctx, "search ai: answered", "event", "search_ai_answered",
		"answered", done.Answered, "sentences", done.Sentences, "dropped", done.Dropped,
		"passages", len(sources), "first_token_ms", done.FirstTokenMs, "took_ms", done.TookMs, "model", model)
	send("done", done)
}

// aiSources finds the passages for a question: the hybrid search under the caller's
// libraries and limits, the best aiPerVideo passages of each of its best aiVideos videos, in
// the search's order, each with the lines around it, up to rules.search.ai_passages.
func (h *Handler) aiSources(ctx context.Context, p *principal.Principal, req AIRequest, rl Rules) ([]AISource, error) {
	f := req.Filters.clone()
	if !p.AccessAllLibraries {
		allowed := map[string]bool{}
		for _, l := range p.Libraries {
			allowed[l] = true
		}
		if len(f.LibraryID) == 0 {
			f.LibraryID = append([]string(nil), p.Libraries...)
		} else {
			var in []string
			for _, l := range f.LibraryID {
				if allowed[l] {
					in = append(in, l)
				}
			}
			f.LibraryID = in
		}
		if len(f.LibraryID) == 0 {
			return []AISource{}, nil
		}
	}
	mode := "hybrid"
	if h.semanticOff() || h.Semantic == nil {
		mode = "fts"
	}
	// the question as written, then, when it finds too few passages (a question's own words,
	// "what are", are in no transcript), each of its content words: the videos that match
	// more of them first
	lists := []aiList{}
	page, err := h.aiSearch(ctx, p, req.Q, mode, f, rl)
	if err != nil {
		return nil, err
	}
	words := questionWords(req.Q)
	lists = append(lists, aiList{page: page, weight: max(1, len(words))})
	asWritten := len(words) == 1 && words[0] == strings.ToLower(strings.TrimSpace(req.Q))
	if passageCount(page) < min(rl.AIPassages, aiWordsBelow) && len(words) > 0 && !asWritten {
		for _, w := range words {
			pw, err := h.aiSearch(ctx, p, w, mode, f, rl)
			if err != nil {
				return nil, err
			}
			lists = append(lists, aiList{page: pw, weight: 1})
		}
	}
	out := fuseAIPages(lists, rl.AIPassages)
	if err := h.aiContext(ctx, out); err != nil {
		return nil, err
	}
	// the prompt's budget: the passages past it are left out, as if never found
	used := 0
	for i := range out {
		used += utf8.RuneCountInString(out[i].context)
		if used > aiPromptRunes && i > 0 {
			out = out[:i]
			break
		}
	}
	for i := range out {
		out[i].N = i + 1
	}
	return out, nil
}

// aiWordsBelow: a question whose own words find fewer passages than this is searched word
// by word as well
const aiWordsBelow = 12

// aiSearch is one search's best aiVideos videos, each with its best aiPerVideo passages.
func (h *Handler) aiSearch(ctx context.Context, p *principal.Principal, q, mode string, f Filters, rl Rules) ([]TranscriptVideo, error) {
	rn, _, err := h.compute(ctx, Request{Q: q, Mode: mode, Filters: f}, rl)
	if err != nil {
		return nil, err
	}
	return h.transcriptPage(ctx, p, rn, 0, aiVideos, aiPerVideo)
}

func passageCount(page []TranscriptVideo) int {
	n := 0
	for _, v := range page {
		n += len(v.Passages)
	}
	return n
}

// aiList is one search's videos and how much it counts: the question as written counts as
// all its words, each word as one.
type aiList struct {
	page   []TranscriptVideo
	weight int
}

// fuseAIPages merges the searches: a video's score is the weight of the searches that
// found it, then their reciprocal ranks; a passage's is the weight of the searches that
// gave it, then its place. The best videos' passages come first, at most limit.
func fuseAIPages(lists []aiList, limit int) []AISource {
	type vid struct {
		v        TranscriptVideo
		weight   int
		rrf      float64
		first    int
		passages map[int64]*aiPassage
		order    []int64
	}
	byID := map[string]*vid{}
	var vids []*vid
	n := 0
	for _, l := range lists {
		for rank, v := range l.page {
			x := byID[v.VideoID]
			if x == nil {
				x = &vid{v: v, first: n, passages: map[int64]*aiPassage{}}
				byID[v.VideoID] = x
				vids = append(vids, x)
			}
			n++
			x.weight += l.weight
			x.rrf += 1 / float64(60+rank+1)
			for i, ps := range v.Passages {
				key := ps.SegmentID
				if key == 0 { // a passage known by its text alone
					key = -int64(len(x.order) + 1)
				}
				pp := x.passages[key]
				if pp == nil {
					pp = &aiPassage{p: ps, place: len(x.order)*100 + i}
					x.passages[key] = pp
					x.order = append(x.order, key)
				}
				pp.weight += l.weight
			}
		}
	}
	sort.SliceStable(vids, func(i, j int) bool {
		a, b := vids[i], vids[j]
		if a.weight != b.weight {
			return a.weight > b.weight
		}
		if a.rrf != b.rrf {
			return a.rrf > b.rrf
		}
		return a.first < b.first
	})
	out := []AISource{}
	for _, x := range vids {
		ps := make([]*aiPassage, 0, len(x.order))
		for _, k := range x.order {
			ps = append(ps, x.passages[k])
		}
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].weight != ps[j].weight {
				return ps[i].weight > ps[j].weight
			}
			return ps[i].place < ps[j].place
		})
		for i, pp := range ps {
			if i >= aiPerVideo || len(out) >= limit {
				break
			}
			out = append(out, AISource{
				VideoID: x.v.VideoID, Title: x.v.Title, LibraryID: x.v.LibraryID, LibraryName: x.v.LibraryName,
				PosterURL: x.v.PosterURL, SegmentID: pp.p.SegmentID, StartSec: pp.p.StartSec, EndSec: pp.p.EndSec,
				Text: plainSnippet(pp.p.Snippet),
			})
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

type aiPassage struct {
	p      Passage
	weight int
	place  int
}

// questionStop are a question's words that say what is asked, not about what: left out of
// the word-by-word search (Arabic and English).
var questionStop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		ما ماذا من متى أين كيف لماذا لم لما هل هي هو هم هن هما أن إن أنه انه ان في على عن إلى الى مع
		ذلك هذا هذه تلك التي الذي الذين اللذين كان كانت يكون تكون أو او و ثم قد لا لن عند كل بعض أي اي
		ليس ليست به بها له لها فيه فيها منه منها بين حول عما مما كم أيضا ايضا يا قال قول اذكر اشرح
		what which who whom whose when where why how is are was were be been being the a an of in on at
		to for from by with about and or nor do does did it its this that these those there here can could
		should would will shall may might must i you he she we they me my your his her our their as if
		than then so not no yes have has had say says said tell explain mean means please any some
		all into onto upon than also just only very`) {
		questionStop[w] = true
	}
}

// questionWords are a question's content words, lower case, each once, at most 8.
func questionWords(q string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.Is(unicode.Mn, r)
	}) {
		if utf8.RuneCountInString(w) < 2 || questionStop[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) == 8 {
			break
		}
	}
	return out
}

// aiContext reads each passage's own words and the line before and after it, from the
// passage's transcript (a passage the meaning search found by its text alone keeps that).
func (h *Handler) aiContext(ctx context.Context, srcs []AISource) error {
	var ids []int64
	for _, s := range srcs {
		if s.SegmentID > 0 {
			ids = append(ids, s.SegmentID)
		}
	}
	lines := map[int64][3]string{}
	if len(ids) > 0 && h.DB != nil {
		var args []any
		rows, err := h.DB.QueryContext(ctx, `
			SELECT s.id, n.seq - s.seq, n.text
			  FROM transcript_segments s
			  JOIN transcript_segments n ON n.transcript_id = s.transcript_id AND n.seq BETWEEN s.seq - 1 AND s.seq + 1
			 WHERE s.id IN (`+placeholders(ids, &args)+`)`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var off int
			var text string
			if err := rows.Scan(&id, &off, &text); err != nil {
				return err
			}
			l := lines[id]
			l[off+1] = strings.TrimSpace(text)
			lines[id] = l
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	for i := range srcs {
		l, ok := lines[srcs[i].SegmentID]
		if ok && l[1] != "" {
			srcs[i].Text = l[1]
		}
		var parts []string
		for _, s := range []string{l[0], srcs[i].Text, l[2]} {
			if s != "" {
				parts = append(parts, s)
			}
		}
		srcs[i].context = clipRunes(strings.Join(parts, " "), aiWindowRunes)
		srcs[i].Text = clipRunes(srcs[i].Text, aiWindowRunes)
	}
	return nil
}

// aiSystem is the model's instructions.
const aiSystem = `You answer questions about a library of recorded talks, using only the numbered passages from their transcripts that you are given.
Rules:
- Answer only from the passages. Add nothing from anywhere else, not even what you know to be true.
- Write a short answer, at most six sentences, in the language of the question.
- End every sentence with the number of the passage it comes from in square brackets, like [2], or [1][3] for two.
- A sentence you cannot support with a passage must not be written.
- If the passages do not answer the question, reply with exactly: ` + aiNoAnswer

// aiMessages is the chat: the instructions, the earlier turns, then the passages and the
// question.
func aiMessages(req AIRequest, srcs []AISource) []AIMessage {
	msgs := []AIMessage{{Role: "system", Content: aiSystem}}
	// the last aiMaxTurns turns that have both a question and an answer
	var turns []AIMessage
	for _, t := range req.History {
		q, a := clipRunes(strings.TrimSpace(t.Q), aiMaxQuestion), clipRunes(strings.TrimSpace(t.Answer), aiMaxAnswerRunes)
		if q == "" || a == "" {
			continue
		}
		turns = append(turns, AIMessage{Role: "user", Content: q}, AIMessage{Role: "assistant", Content: a})
	}
	if len(turns) > 2*aiMaxTurns {
		turns = turns[len(turns)-2*aiMaxTurns:]
	}
	msgs = append(msgs, turns...)
	var b strings.Builder
	b.WriteString("Passages:\n")
	for _, s := range srcs {
		fmt.Fprintf(&b, "[%d] (%s, %s) %s\n", s.N, s.Title, videoClock(s.StartSec), s.context)
	}
	b.WriteString("\nQuestion: ")
	b.WriteString(req.Q)
	msgs = append(msgs, AIMessage{Role: "user", Content: b.String()})
	return msgs
}

// videoClock is a time in a video as h:mm:ss or m:ss.
func videoClock(sec float64) string {
	s := int(sec)
	if s < 0 {
		s = 0
	}
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// citation markers: [2], [1, 3], [1][3], [passage 2], with Arabic-Indic digits read as digits
var citeRe = regexp.MustCompile(`(?i)\[\s*(?:passages?\s*)?([0-9٠-٩۰-۹]+(?:\s*[,،]\s*[0-9٠-٩۰-۹]+)*)\s*\]`)

var spaceRe = regexp.MustCompile(`\s+`)

// citeSentence checks one sentence: it keeps the citations that name a given passage, and
// is dropped (false) when none does. The markers are taken out of its text.
func citeSentence(sentence string, srcs []AISource) (AITextEvent, bool) {
	s := sentence // the words as the model wrote them; only a marker's digits are read as ASCII
	seen := map[int]bool{}
	ev := AITextEvent{Citations: []AICitation{}}
	for _, m := range citeRe.FindAllStringSubmatch(s, -1) {
		for _, part := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == '،' || unicode.IsSpace(r) }) {
			n, err := strconv.Atoi(asciiDigits(part))
			if err != nil || n < 1 || n > len(srcs) || seen[n] || len(ev.Citations) >= aiMaxCitations {
				continue
			}
			seen[n] = true
			src := srcs[n-1]
			ev.Citations = append(ev.Citations, AICitation{
				N: n, VideoID: src.VideoID, StartSec: src.StartSec, EndSec: src.EndSec, Quote: src.Text,
			})
		}
	}
	sort.SliceStable(ev.Citations, func(i, j int) bool { return ev.Citations[i].N < ev.Citations[j].N })
	text := citeRe.ReplaceAllString(s, "")
	text = strings.TrimSpace(spaceRe.ReplaceAllString(text, " "))
	for _, p := range []string{" .", " ,", " ؟", " ?", " !", " ،", " ؛"} {
		text = strings.ReplaceAll(text, p, p[1:])
	}
	ev.Text = text
	if len(ev.Citations) == 0 || strings.Trim(text, ".!?؟،,;؛:-–— ") == "" {
		return AITextEvent{}, false
	}
	return ev, true
}

// asciiDigits writes Arabic-Indic and Persian digits as ASCII ones.
func asciiDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '٠' && r <= '٩':
			return '0' + (r - '٠')
		case r >= '۰' && r <= '۹':
			return '0' + (r - '۰')
		}
		return r
	}, s)
}

// sentenceSplitter cuts the model's stream into sentences: a sentence ends at . ! ? ؟ ۔ or a
// line break, with the citation markers after it, once the next sentence has begun.
type sentenceSplitter struct{ buf string }

func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', '؟', '۔', '\n':
		return true
	}
	return false
}

// add takes a piece of the stream and returns the sentences it completed.
func (s *sentenceSplitter) add(piece string) []string {
	s.buf += piece
	var out []string
	for {
		cut := s.cut()
		if cut <= 0 {
			return out
		}
		if t := strings.TrimSpace(s.buf[:cut]); t != "" {
			out = append(out, t)
		}
		s.buf = s.buf[cut:]
	}
}

// cut is where the buffer's first complete sentence ends (0: none yet).
func (s *sentenceSplitter) cut() int {
	b := s.buf
	for i, r := range b {
		if !isSentenceEnd(r) {
			continue
		}
		j := i + utf8.RuneLen(r)
		if r == '.' {
			if next, _ := utf8.DecodeRuneInString(b[j:]); unicode.IsDigit(next) {
				continue // 3.5, ٣.٥
			}
		}
		// the markers and spaces after the end belong to this sentence
		for {
			k := j
			for k < len(b) && (b[k] == ' ' || b[k] == '\t') {
				k++
			}
			if k < len(b) && b[k] == '[' {
				end := strings.IndexByte(b[k:], ']')
				if end < 0 {
					return 0 // a marker still arriving
				}
				j = k + end + 1
				continue
			}
			if next, size := utf8.DecodeRuneInString(b[k:]); k < len(b) && isSentenceEnd(next) && next != '\n' {
				j = k + size // "… [2]." : the end after the marker
				continue
			}
			j = k
			break
		}
		if j >= len(b) {
			return 0 // more markers may come
		}
		return j
	}
	return 0
}

// flush returns what is left at the end of the stream.
func (s *sentenceSplitter) flush() string {
	t := strings.TrimSpace(s.buf)
	s.buf = ""
	return t
}

// plainSnippet is a snippet's text without its <mark> tags and the ellipses a cut adds.
func plainSnippet(s string) string {
	s = strings.NewReplacer("<mark>", "", "</mark>", "").Replace(s)
	return strings.TrimSpace(strings.Trim(s, "…"))
}

// clipRunes cuts s to at most n runes.
func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// aiLine is AI mode's line: one answer at a time per home, at most aiMaxWaiting waiting.
type aiLine struct {
	mu      sync.Mutex
	busy    bool
	waiting []*aiTicket
}

// aiTicket is one question's place: turn closes when it may be answered; pos carries its
// place while it waits (the newest only).
type aiTicket struct {
	turn    chan struct{}
	pos     chan int
	holding bool
}

// join takes a place: 0 when the question is answered now, else its place in line; false
// when the line is full (the place it would have had).
func (l *aiLine) join() (*aiTicket, int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := &aiTicket{turn: make(chan struct{}), pos: make(chan int, 1)}
	if !l.busy {
		l.busy, t.holding = true, true
		close(t.turn)
		return t, 0, true
	}
	if len(l.waiting) >= aiMaxWaiting {
		return nil, len(l.waiting) + 1, false
	}
	l.waiting = append(l.waiting, t)
	n := len(l.waiting)
	t.pos <- n
	return t, n, true
}

// leave gives the turn to the next in line (or leaves the line) and tells the others their
// new place.
func (l *aiLine) leave(t *aiTicket) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.holding {
		t.holding = false
		if len(l.waiting) == 0 {
			l.busy = false
			return
		}
		next := l.waiting[0]
		l.waiting = l.waiting[1:]
		next.holding = true
		close(next.turn)
	} else {
		for i, w := range l.waiting {
			if w == t {
				l.waiting = append(l.waiting[:i], l.waiting[i+1:]...)
				break
			}
		}
	}
	for i, w := range l.waiting {
		select {
		case <-w.pos:
		default:
		}
		w.pos <- i + 1
	}
}

// ollama asks Ollama's API: /api/tags for Ready, /api/chat (streamed) for Chat.
type ollama struct{ client *http.Client }

var defaultOllama AIModel = ollama{client: &http.Client{}}

func (o ollama) Ready(ctx context.Context, server, model string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/tags", nil)
	if err != nil {
		return err
	}
	res, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errAIUnavailable, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: tags %d", errAIUnavailable, res.StatusCode)
	}
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tags); err != nil {
		return fmt.Errorf("%w: tags: %v", errAIUnavailable, err)
	}
	for _, m := range tags.Models {
		for _, n := range []string{m.Name, m.Model} {
			if n == model || n == model+":latest" {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %s is not on %s", errAIUnavailable, model, server)
}

func (o ollama) Chat(ctx context.Context, server, model string, msgs []AIMessage, piece func(string) error) error {
	body, _ := json.Marshal(map[string]any{
		"model": model, "messages": msgs, "stream": true,
		"options": map[string]any{"temperature": 0.2, "num_ctx": 16384},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/chat", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("chat: %d", res.StatusCode)
	}
	dec := json.NewDecoder(res.Body)
	for {
		var line struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done  bool   `json:"done"`
			Error string `json:"error"`
		}
		if err := dec.Decode(&line); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("chat: %w", err)
		}
		if line.Error != "" {
			return fmt.Errorf("chat: %s", line.Error)
		}
		if line.Message.Content != "" {
			if err := piece(line.Message.Content); err != nil {
				return err
			}
		}
		if line.Done {
			return nil
		}
	}
}
