package logindex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// logTo keeps the index's own log in the test's report.
func logTo(t testing.TB, ix *Index) {
	t.Helper()
	var mu sync.Mutex
	done := false
	t.Cleanup(func() { mu.Lock(); done = true; mu.Unlock() })
	ix.Log = func(msg string, kv ...any) {
		mu.Lock()
		defer mu.Unlock()
		if !done {
			t.Logf("logindex: %s %v", msg, kv)
		}
	}
}

// closedHours writes services × hours closed (.zst) segments of n lines each, and one open
// (.ndjson) hour per service, as the keeper leaves them; it returns the .zst paths.
func closedHours(t testing.TB, root string, services, hours, n int) []string {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	var zsts []string
	start := t0.Truncate(time.Hour).Add(-time.Duration(hours) * time.Hour)
	for s := 0; s < services; s++ {
		svc := fmt.Sprintf("svc%02d", s)
		dir := filepath.Join(root, "segments", svc)
		if err := os.MkdirAll(filepath.Join(dir, "warn"), 0o700); err != nil {
			t.Fatal(err)
		}
		for h := 0; h <= hours; h++ {
			at := start.Add(time.Duration(h) * time.Hour)
			var b strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, `{"ts":%q,"level":"info","service":%q,"msg":"line %d"}`+"\n",
					at.Add(time.Duration(i)*time.Second).Format(time.RFC3339Nano), svc, i)
			}
			base := filepath.Join(dir, at.Format("20060102-15"))
			_ = os.WriteFile(base+".idx", []byte(`{}`), 0o600)
			if h == hours {
				if err := os.WriteFile(base+".ndjson", []byte(b.String()), 0o600); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err := os.WriteFile(base+".ndjson.zst", enc.EncodeAll([]byte(b.String()), nil), 0o600); err != nil {
				t.Fatal(err)
			}
			zsts = append(zsts, base+".ndjson.zst")
		}
	}
	return zsts
}

// v0.3.135 ran two cores flat for a day: every 200 ms pass decompressed every closed hour
// (a week of them) from its start to find nothing new. A closed hour read to its end is
// never opened again, across a restart too, and an idle pass does bounded work.
func TestIdlePassOpensNoClosedHour(t *testing.T) {
	root, now := t.TempDir(), t0
	zsts := closedHours(t, root, 2, 12, 3)
	ix := open(t, root, &now)
	logTo(t, ix)
	ctx := context.Background()
	if n, err := ix.Ingest(ctx); err != nil || n != 2*13*3 {
		t.Fatalf("first pass: %d %v", n, err)
	}
	// were a closed hour read again, it would not decode: the pass would log it
	unreadable := 0
	ix.Log = func(msg string, kv ...any) {
		t.Logf("logindex: %s %v", msg, kv)
		if msg == "log_index_segment_unreadable" {
			unreadable++
		}
	}
	for _, p := range zsts {
		if err := os.WriteFile(p, []byte("not zstd"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if n, err := ix.Ingest(ctx); err != nil || n != 0 {
			t.Fatalf("idle pass %d: %d %v", i, n, err)
		}
	}
	if unreadable != 0 {
		t.Fatalf("an idle pass opened %d closed hours", unreadable)
	}
	// a restart reads the marks from logs.db: still nothing opened
	_ = ix.Close()
	ix = open(t, root, &now)
	ix.Log = func(msg string, kv ...any) {
		t.Logf("logindex: %s %v", msg, kv)
		if msg == "log_index_segment_unreadable" {
			unreadable++
		}
	}
	if n, err := ix.Ingest(ctx); err != nil || n != 0 || unreadable != 0 {
		t.Fatalf("after a restart: %d %v, %d closed hours opened", n, err, unreadable)
	}
	// and an idle pass costs a listing, not a file each: bounded allocations per segment
	allocs := testing.AllocsPerRun(5, func() { _, _ = ix.Ingest(ctx) })
	if per := allocs / float64(len(zsts)); per > 12 {
		t.Fatalf("an idle pass allocated %.0f times (%.1f per segment)", allocs, per)
	}
}

// An open hour is read on from its position, never again from 0; once it closes (.zst) it
// is read once, to check its end, and then left alone.
func TestOpenHourReadsOnFromItsPosition(t *testing.T) {
	root, now := t.TempDir(), t0
	closedHours(t, root, 1, 1, 3)
	ix := open(t, root, &now)
	logTo(t, ix)
	ctx := context.Background()
	if n, err := ix.Ingest(ctx); err != nil || n != 6 {
		t.Fatalf("first pass: %d %v", n, err)
	}
	open := filepath.Join(root, "segments", "svc00", t0.Truncate(time.Hour).Format("20060102-15")+".ndjson")
	f, err := os.OpenFile(open, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, `{"ts":%q,"level":"info","service":"svc00","msg":"four"}`+"\n", t0.Format(time.RFC3339Nano))
	_ = f.Close()
	if n, err := ix.Ingest(ctx); err != nil || n != 1 {
		t.Fatalf("an appended line: %d %v (more than 1 is the hour read again from 0)", n, err)
	}
	// the hour closes: compressed whole, the .ndjson goes
	b, _ := os.ReadFile(open)
	enc, _ := zstd.NewWriter(nil)
	_ = os.WriteFile(open+".zst", enc.EncodeAll(b, nil), 0o600)
	_ = enc.Close()
	_ = os.Remove(open)
	if n, err := ix.Ingest(ctx); err != nil || n != 0 {
		t.Fatalf("the closed hour: %d %v", n, err)
	}
	key := "svc00/" + t0.Truncate(time.Hour).Format("20060102-15")
	if p := ix.pos[key]; !p.done || p.off != int64(len(b)) {
		t.Fatalf("the closed hour's position: %+v, want done at %d", p, len(b))
	}
	var size int64
	if err := ix.db.QueryRow(`SELECT size FROM positions WHERE seg = ?`, key).Scan(&size); err != nil || size <= 0 {
		t.Fatalf("positions.size %d %v: the mark is not kept for a restart", size, err)
	}
	res, err := ix.Search(ctx, Query{Q: "four", Since: t0.Add(-48 * time.Hour), Until: t0.Add(time.Hour)})
	if err != nil || len(res.Lines) != 1 {
		t.Fatalf("search four: %d %v", len(res.Lines), err)
	}
}

// v0.3.135 lost metric scrapes with "database is locked (5) (SQLITE_BUSY)" while the sweep
// held logs.db's write lock past busy_timeout. The process is logs.db's one writer: the
// scraper waits its turn, however long the other write takes.
func TestRecordWaitsForAnotherWriter(t *testing.T) {
	old := busyTimeout
	busyTimeout = 20 // ms: far shorter than the other write below
	t.Cleanup(func() { busyTimeout = old })
	root, now := t.TempDir(), t0
	ix := open(t, root, &now)
	logTo(t, ix)
	ctx := context.Background()
	holding := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := ix.write(func() error {
			tx, err := ix.db.Begin()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO metric_1m(name, labels, minute, value) VALUES('x', '', 1, 1)`); err != nil {
				_ = tx.Rollback()
				return err
			}
			close(holding)
			time.Sleep(300 * time.Millisecond) // a sweep's long delete, a checkpoint
			return tx.Commit()
		})
		if err != nil {
			t.Error(err)
		}
	}()
	<-holding
	at := t0
	for i := 0; i < 3; i++ {
		if err := ix.Record(ctx, []sample{{name: "maktaba_logs_index_bytes", value: float64(i)}}, at); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		if err := ix.RecordSizes(ctx, at); err != nil {
			t.Fatalf("record sizes %d: %v", i, err)
		}
	}
	wg.Wait()
	var v float64
	if err := ix.db.QueryRow(`SELECT value FROM metric_1m WHERE name = 'maktaba_logs_index_bytes' AND minute = ?`,
		minuteOf(at)).Scan(&v); err != nil {
		t.Fatal(err)
	}
}

// Metrics count against logs.db's cap: over it, minutes older than a day go (their hours
// stay, and a long range reads them) before any info line. v0.3.135 evicted lines only, so
// a week of minutes kept the file over the cap and every info line went.
func TestCapTrimsOldMinutesBeforeInfoLines(t *testing.T) {
	root, now := t.TempDir(), t0
	closedHours(t, root, 1, 2, 10)
	ix := open(t, root, &now)
	logTo(t, ix)
	ctx := context.Background()
	if _, err := ix.Ingest(ctx); err != nil {
		t.Fatal(err)
	}
	var lines int
	_ = ix.db.QueryRow(`SELECT COUNT(*) FROM lines`).Scan(&lines)
	// a day and a half of minutes (one in thirty written), two series with long labels: the bulk of
	// the file, in few rows (the unit tier runs under -race)
	err := ix.write(func() error {
		tx, err := ix.db.Begin()
		if err != nil {
			return err
		}
		st, err := tx.Prepare(`INSERT INTO metric_1m(name, labels, minute, value) VALUES(?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		from := now.Add(-36*time.Hour).Unix() / 60
		pad := strings.Repeat("x", 8000)
		for m := from; m < now.Unix()/60; m += 30 {
			for s := 0; s < 2; s++ {
				if _, err := st.Exec("maktaba_component_up", fmt.Sprintf(`component="c%d",pad="%s"`, s, pad), m, 1); err != nil {
					return err
				}
			}
		}
		return tx.Commit()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Rollup(ctx); err != nil { // the hours are in the file before it is measured
		t.Fatal(err)
	}
	_, _ = ix.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	// minutes older than a day are about a fifth of the file: dropping them fits, the info
	// lines (a sliver) would not be enough
	ix.MaxBytes = ix.Size() * 90 / 100
	if _, err := ix.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if ix.Size() > ix.MaxBytes {
		t.Fatalf("still over the cap: %d > %d", ix.Size(), ix.MaxBytes)
	}
	var left, oldest int64
	_ = ix.db.QueryRow(`SELECT COUNT(*) FROM lines`).Scan(&left)
	if int(left) != lines {
		t.Fatalf("info lines evicted: %d of %d left", left, lines)
	}
	_ = ix.db.QueryRow(`SELECT MIN(minute) FROM metric_1m`).Scan(&oldest)
	// the oldest minutes went, an hour at a time, until the file fit; none of the last day
	from := now.Add(-36*time.Hour).Unix() / 60
	if dayAgo := now.Add(-24*time.Hour).Unix() / 60; oldest <= from+60 || oldest > dayAgo {
		t.Fatalf("minutes kept from %d (written from %d, a day ago %d)", oldest, from, dayAgo)
	}
	var hours int64
	_ = ix.db.QueryRow(`SELECT COUNT(DISTINCT hour) FROM metric_1h WHERE hour < ?`, oldest/60).Scan(&hours)
	if hours < (oldest-from)/60 {
		t.Fatalf("the trimmed minutes' hours: %d", hours)
	}
	// the 7-day board now reads the hours (the minutes start a day ago)
	if ix.minutesSince(ctx, now.Add(-7*24*time.Hour)) {
		t.Fatal("a week's range still reads the minutes")
	}
	if !ix.minutesSince(ctx, now.Add(-time.Hour)) {
		t.Fatal("an hour's range reads the hours")
	}
}

// BenchmarkIdlePass is one idle pass over a week of 20 services' closed hours (the live
// home's shape): it lists directories and stats the open hours, nothing more.
func BenchmarkIdlePass(b *testing.B) {
	root, now := b.TempDir(), t0
	closedHours(b, root, 20, 7*24, 5)
	ix, err := Open(root, func() time.Time { return now })
	if err != nil {
		b.Fatal(err)
	}
	defer ix.Close()
	ctx := context.Background()
	if _, err := ix.Ingest(ctx); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n, err := ix.Ingest(ctx); err != nil || n != 0 {
			b.Fatal(n, err)
		}
	}
}
