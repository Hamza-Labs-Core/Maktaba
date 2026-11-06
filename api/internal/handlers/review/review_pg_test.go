package review

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	tt "github.com/Hamza-Labs-Core/Maktaba/api/internal/transcript/transcripttest"
)

type fakeSigner struct{}

func (fakeSigner) SignStreamURL(raw, _, _, _ string, ttl time.Duration) (string, time.Time, error) {
	return raw + "&sig=x", time.Now().Add(ttl), nil
}

type pgf struct {
	t  *testing.T
	db *sql.DB
	r  chi.Router
	w  tt.World
}

func newPG(t *testing.T, sentences ...tt.Sentence) *pgf {
	db, _ := clustertest.ScratchDB(t)
	f := &pgf{t: t, db: db, r: chi.NewRouter()}
	f.w = tt.Seed(t, db, sentences...)
	(&Handler{DB: db, Signer: fakeSigner{}}).Mount(f.r)
	return f
}

func (f *pgf) do(method, path, body, user string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: user, IsAdmin: true, AccessAllLibraries: true}))
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

func (f *pgf) json(rec *httptest.ResponseRecorder, want int, v any) {
	f.t.Helper()
	if rec.Code != want {
		f.t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
	if v != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			f.t.Fatal(err)
		}
	}
}

// A home admin lists every library's open flags by priority, cursor paged; opening one
// locks it for 5 minutes; another admin then gets 409 with the holder's name, unless
// they force it; a correction edits the transcript and its undo reopens the flag.
func TestListLockResolveUndo(t *testing.T) {
	f := newPG(t, tt.Sentence{Text: "قال الصلوة خير", WithWords: true}, tt.Sentence{Text: "teh sky", Start: 5, WithWords: true})
	low := tt.Flag(t, f.db, f.w, 1, 0, 3, "teh", "teh", "en", 0.55)
	high := tt.Flag(t, f.db, f.w, 0, 4, 6, "الصلوة", "الصلوه", "ar", 0.9, "quran_mismatch")
	var page struct {
		Items      []Flag  `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	f.json(f.do("GET", "/api/review/flags?limit=1", "", f.w.AdminID), 200, &page)
	if len(page.Items) != 1 || page.Items[0].ID != high || page.NextCursor == nil || page.Items[0].Direction != "rtl" {
		t.Fatalf("first page %+v", page)
	}
	f.json(f.do("GET", "/api/review/flags?limit=1&cursor="+*page.NextCursor, "", f.w.AdminID), 200, &page)
	if len(page.Items) != 1 || page.Items[0].ID != low {
		t.Fatalf("second page %+v", page)
	}
	var d Detail
	f.json(f.do("GET", "/api/review/flags/"+high, "", f.w.AdminID), 200, &d)
	if d.LockedBy == nil || *d.LockedBy != f.w.AdminID || d.After != "teh sky" {
		t.Fatalf("detail %+v", d)
	}
	// the other admin: the locked flag is not in the list, resolving it is 409
	f.json(f.do("GET", "/api/review/flags", "", f.w.OtherAdminID), 200, &page)
	if len(page.Items) != 1 || page.Items[0].ID != low {
		t.Fatalf("other admin's list %+v", page)
	}
	rec := f.do("POST", "/api/review/flags/"+high+"/resolve", `{"action":"accept"}`, f.w.OtherAdminID)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"locked"`) || !strings.Contains(rec.Body.String(), "admin-a") {
		t.Fatalf("locked: %d %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	f.json(f.do("POST", "/api/review/flags/"+high+"/resolve", `{"action":"correct","text":"الصلاة"}`, f.w.AdminID), 200, &res)
	if res["status"] != "corrected" || res["edit_id"] == nil {
		t.Fatalf("resolve %v", res)
	}
	var text string
	_ = f.db.QueryRow(`SELECT text FROM transcript_segments WHERE transcript_id = $1 AND seq = 0`, f.w.TranscriptID).Scan(&text)
	if text != "قال الصلاة خير" {
		t.Fatalf("text %q", text)
	}
	var hist struct{ Items []EditRow }
	f.json(f.do("GET", "/api/videos/"+f.w.VideoID+"/transcript/edits", "", f.w.OtherAdminID), 200, &hist)
	if len(hist.Items) != 1 || hist.Items[0].After != "الصلاة" {
		t.Fatalf("history %+v", hist)
	}
	// any admin may undo any edit
	f.json(f.do("POST", "/api/review/edits/"+res["edit_id"].(string)+"/undo", "", f.w.OtherAdminID), 200, nil)
	var status string
	_ = f.db.QueryRow(`SELECT status FROM word_flags WHERE id = $1`, high).Scan(&status)
	if status != "open" {
		t.Fatalf("after undo %s", status)
	}
	// validation
	for _, body := range []string{`{"action":"correct","text":""}`, `{"action":"rewrite"}`} {
		if rec := f.do("POST", "/api/review/flags/"+low+"/resolve", body, f.w.AdminID); rec.Code != 400 {
			t.Fatalf("%s: %d", body, rec.Code)
		}
	}
	// a stale flag: 409 stale
	_, _ = f.db.Exec(`UPDATE word_flags SET status = 'stale' WHERE id = $1`, low)
	if rec := f.do("POST", "/api/review/flags/"+low+"/resolve", `{"action":"correct","text":"the"}`, f.w.AdminID); rec.Code != 409 || !strings.Contains(rec.Body.String(), "stale") {
		t.Fatalf("stale: %d %s", rec.Code, rec.Body.String())
	}
	var clip map[string]any
	f.json(f.do("GET", "/api/review/flags/"+high+"/clip", "", f.w.AdminID), 200, &clip)
	if !strings.Contains(clip["url"].(string), "/stream/clip/"+f.w.VideoID+"?track=1&start=0.00&end=3.00") {
		t.Fatalf("clip %v", clip)
	}
}

// Apply to every place: one batch, one undo; the third time a correction is made its
// spelling joins the vocabulary.
func TestSameKeyBatchAndLearning(t *testing.T) {
	f := newPG(t, tt.Sentence{Text: "a teh b", WithWords: true}, tt.Sentence{Text: "teh c", Start: 5, WithWords: true},
		tt.Sentence{Text: "d teh", Start: 10, WithWords: true})
	first := tt.Flag(t, f.db, f.w, 0, 2, 3, "teh", "teh", "en", 0.8)
	tt.Flag(t, f.db, f.w, 1, 0, 3, "teh", "teh", "en", 0.7)
	tt.Flag(t, f.db, f.w, 2, 2, 3, "teh", "teh", "en", 0.6)
	var d Detail
	f.json(f.do("GET", "/api/review/flags/"+first, "", f.w.AdminID), 200, &d)
	if d.SameKeyOpen != 2 || len(d.SameKeyPreview) != 2 {
		t.Fatalf("same key %d %v", d.SameKeyOpen, d.SameKeyPreview)
	}
	var res map[string]any
	f.json(f.do("POST", "/api/review/flags/"+first+"/resolve", `{"action":"correct","text":"the","apply_to_same_key":true}`, f.w.AdminID), 200, &res)
	if res["changed"].(float64) != 3 || res["batch_id"] == nil {
		t.Fatalf("batch %v", res)
	}
	var corrected int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM word_flags WHERE status = 'corrected'`).Scan(&corrected)
	if corrected != 3 {
		t.Fatalf("%d corrected", corrected)
	}
	f.json(f.do("POST", "/api/review/batches/"+res["batch_id"].(string)+"/undo", "", f.w.AdminID), 200, &res)
	if res["undone"].(float64) != 3 {
		t.Fatalf("undo %v", res)
	}
	var texts []string
	rows, _ := f.db.Query(`SELECT text FROM transcript_segments WHERE transcript_id = $1 ORDER BY seq`, f.w.TranscriptID)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		texts = append(texts, s)
	}
	_ = rows.Close()
	if strings.Join(texts, "|") != "a teh b|teh c|d teh" {
		t.Fatalf("after the batch undo %v", texts)
	}
	// three corrections of the same word, one at a time: the third teaches the vocabulary
	var ids []string
	rs, _ := f.db.Query(`SELECT id::text FROM word_flags ORDER BY segment_seq`)
	for rs.Next() {
		var id string
		_ = rs.Scan(&id)
		ids = append(ids, id)
	}
	_ = rs.Close()
	for _, id := range ids {
		f.json(f.do("POST", "/api/review/flags/"+id+"/resolve", `{"action":"correct","text":"the"}`, f.w.AdminID), 200, nil)
	}
	var uses, words int
	_ = f.db.QueryRow(`SELECT uses FROM vocab_pairs WHERE heard_key = 'teh'`).Scan(&uses)
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM vocabulary WHERE term = 'the' AND kind = 'word'`).Scan(&words)
	if uses < 3 || words != 1 {
		t.Fatalf("pairs %d, vocabulary %d", uses, words)
	}
	var sum map[string]any
	f.json(f.do("GET", "/api/review/summary", "", f.w.AdminID), 200, &sum)
	if _, ok := sum["libraries"]; !ok {
		t.Fatalf("summary %v", sum)
	}
}

// A name joins the vocabulary and re-checks the transcripts with that word open; fixing a
// word from the transcript makes a manual flag; the library's settings validate and
// re-check its transcripts; imports report their bad rows.
func TestNameFixSettingsAndImport(t *testing.T) {
	f := newPG(t, tt.Sentence{Text: "قال عثيمين في الدرس", WithWords: true})
	flag := tt.Flag(t, f.db, f.w, 0, 4, 6, "عثيمين", "عثيمين", "ar", 0.6)
	// the same word open elsewhere: the name re-checks its transcript
	tt.Flag(t, f.db, f.w, 0, 15, 5, "الدرس", "عثيمين", "ar", 0.5)
	f.json(f.do("POST", "/api/review/flags/"+flag+"/resolve", `{"action":"name"}`, f.w.AdminID), 200, nil)
	var kind string
	_ = f.db.QueryRow(`SELECT kind FROM vocabulary WHERE key = 'عثيمين' AND library_id = $1`, f.w.LibraryID).Scan(&kind)
	var checks int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE stage = 'word_check' AND subject_id = $1`, f.w.VideoID).Scan(&checks)
	if kind != "name" || checks != 1 {
		t.Fatalf("name: kind %q, %d checks", kind, checks)
	}
	var fix map[string]any
	f.json(f.do("POST", "/api/videos/"+f.w.VideoID+"/transcript/words",
		`{"segment_seq":0,"char_start":11,"char_len":2,"before":"في","text":"فى"}`, f.w.AdminID), 200, &fix)
	if fix["text"] != "قال عثيمين فى الدرس" {
		t.Fatalf("fix %v", fix)
	}
	var reasons, status string
	_ = f.db.QueryRow(`SELECT array_to_string(reasons, ','), status FROM word_flags WHERE id = $1`, fix["flag_id"]).Scan(&reasons, &status)
	if reasons != "manual" || status != "corrected" {
		t.Fatalf("manual flag %s %s", reasons, status)
	}
	if rec := f.do("POST", "/api/videos/"+f.w.VideoID+"/transcript/words",
		`{"segment_seq":0,"char_start":11,"char_len":2,"before":"في","text":"x"}`, f.w.AdminID); rec.Code != 409 {
		t.Fatalf("stale fix: %d %s", rec.Code, rec.Body.String())
	}
	for body, field := range map[string]string{`{"budget_per_hour":300}`: "budget_per_hour", `{"reasons":{"llm_disagrees":false}}`: "reasons.llm_disagrees"} {
		rec := f.do("PUT", "/api/libraries/"+f.w.LibraryID+"/word-review", body, f.w.AdminID)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), field) {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	f.json(f.do("PUT", "/api/libraries/"+f.w.LibraryID+"/word-review", `{"sensitivity":"low","backfill":false}`, f.w.AdminID), 200, nil)
	var view WordReviewView
	f.json(f.do("GET", "/api/libraries/"+f.w.LibraryID+"/word-review", "", f.w.AdminID), 200, &view)
	if view.Settings.Sensitivity == nil || *view.Settings.Sensitivity != "low" || view.Backfill.Transcripts != 1 {
		t.Fatalf("settings %+v", view)
	}
	var recheck int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM processing_jobs WHERE stage = 'word_check' AND subject_kind = 'library'`).Scan(&recheck)
	if recheck != 1 {
		t.Fatalf("%d rechecks", recheck)
	}
	req := httptest.NewRequest("POST", "/api/vocabulary/import?scope=library&library="+f.w.LibraryID,
		strings.NewReader("term,kind,language,variants\nابن تيمية,name,ar,ابن تيميه\n,word,ar,\nx,nonsense,ar,\n"))
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: f.w.AdminID, IsAdmin: true}))
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	var imp struct {
		Added int `json:"added"`
		Bad   []struct {
			Line   int    `json:"line"`
			Reason string `json:"reason"`
		} `json:"bad"`
	}
	f.json(rec, 200, &imp)
	if imp.Added != 1 || len(imp.Bad) != 2 || imp.Bad[0].Line != 3 {
		t.Fatalf("import %+v", imp)
	}
	exp := f.do("GET", "/api/vocabulary/export", "", f.w.AdminID)
	if !strings.HasPrefix(exp.Body.String(), "term,kind,language,variants,scope,library_id,uses") || !strings.Contains(exp.Body.String(), "ابن تيمية") {
		t.Fatalf("export %s", exp.Body.String())
	}
	var packs LanguagePacksBody
	f.json(f.do("GET", "/api/language-packs", "", f.w.AdminID), 200, &packs)
	if packs.Heard["ar"] != 1 {
		t.Fatalf("packs %+v", packs)
	}
}

// WordReviewView and LanguagePacksBody are the tests' reading of the answers.
type WordReviewView struct {
	Settings Settings `json:"settings"`
	Backfill struct {
		Transcripts int `json:"transcripts"`
	} `json:"backfill"`
}

type LanguagePacksBody struct {
	Heard map[string]int `json:"heard"`
}
