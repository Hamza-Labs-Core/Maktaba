package pgbundle

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lines of the outage's dump (pre-0.3.41-to-0.3.42.dump, pg_restore -l), one of each kind
// the restore splits or checks.
const outageTOC = `;
; Archive created at 2026-10-28 17:22:43 EDT
;
6067; 0 0 ACL - SCHEMA public pg_database_owner
6072; 0 0 ACL pg_catalog FUNCTION pg_copy_logical_replication_slot(src_slot_name name, dst_slot_name name, OUT slot_name name, OUT lsn pg_lsn) maktaba_admin
451; 1255 698504 FUNCTION public app_settings_notify() maktaba
237; 1259 698655 VIEW public channel_programs_live maktaba
5843; 0 698547 TABLE DATA public api_usage_daily maktaba
4697; 2606 768935 CONSTRAINT public api_usage_daily api_usage_daily_pkey maktaba
4567; 2606 768391 CHECK CONSTRAINT public audit_log audit_log_category_check maktaba
5331; 2606 768962 FK CONSTRAINT public api_usage_people_daily api_usage_people_daily_user_id_fkey maktaba
4704; 1259 768701 INDEX public audio_cache_video_idx maktaba
5524; 2620 768603 TRIGGER public app_settings app_settings_notify_trg maktaba
6377; 0 0 SEQUENCE SET public audio_tracks_id_seq maktaba
6137; 0 0 SEQUENCE OWNED BY public audio_tracks_id_seq maktaba
5775; 6106 786192 PUBLICATION TABLE public maktaba_copy_1e943eda006442e98692851ebab36f54 app_settings maktaba
6086; 0 0 ACL public FUNCTION app_settings_notify() maktaba
6132; 0 0 ACL public TABLE api_usage_daily maktaba
6168; 0 0 ACL public COLUMN cluster_nodes.id maktaba
3213; 826 785962 DEFAULT ACL public DEFAULT PRIVILEGES FOR TABLES maktaba
;6068; 0 0 COMMENT - EXTENSION pg_trgm
`

func TestParseTOC(t *testing.T) {
	got := map[string]TOCEntry{}
	for _, e := range ParseTOC(outageTOC) {
		got[e.Desc+" "+e.Name] = e
	}
	for key, want := range map[string]TOCEntry{
		"ACL SCHEMA public":                                                        {Schema: "-", Owner: "pg_database_owner"},
		"FUNCTION app_settings_notify()":                                           {Schema: "public", Owner: "maktaba"},
		"TABLE DATA api_usage_daily":                                               {Schema: "public", Owner: "maktaba"},
		"CONSTRAINT api_usage_daily api_usage_daily_pkey":                          {Schema: "public", Owner: "maktaba"},
		"FK CONSTRAINT api_usage_people_daily api_usage_people_daily_user_id_fkey": {Schema: "public", Owner: "maktaba"},
		"TRIGGER app_settings app_settings_notify_trg":                             {Schema: "public", Owner: "maktaba"},
		"SEQUENCE OWNED BY audio_tracks_id_seq":                                    {Schema: "public", Owner: "maktaba"},
		"ACL COLUMN cluster_nodes.id":                                              {Schema: "public", Owner: "maktaba"},
		"DEFAULT ACL DEFAULT PRIVILEGES FOR TABLES":                                {Schema: "public", Owner: "maktaba"},
	} {
		e, ok := got[key]
		if !ok || e.Schema != want.Schema || e.Owner != want.Owner {
			t.Errorf("%s: %+v (%v)", key, e, ok)
		}
	}
	if _, ok := got["COMMENT EXTENSION pg_trgm"]; ok {
		t.Error("a line commented out is listed")
	}
}

// The grants and default privileges go in a list of their own, restored last; every other
// item in the main one (what is commented out stays so in both).
func TestRestoreListsSplitTheGrants(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "d.dump")
	main, _, acls, err := writeRestoreLists(dump, outageTOC)
	if err != nil {
		t.Fatal(err)
	}
	read := func(p string) (kept []string) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l != "" && !strings.HasPrefix(l, ";") {
				kept = append(kept, l)
			}
		}
		return kept
	}
	m, a := read(main), read(acls)
	if len(a) != 6 || len(m) != 11 {
		t.Fatalf("main %d items, grants %d items:\n%s\n--\n%s", len(m), len(a), strings.Join(m, "\n"), strings.Join(a, "\n"))
	}
	for _, l := range a {
		if !isACL(l) {
			t.Errorf("not a grant in the grants' list: %s", l)
		}
	}
	for _, l := range m {
		if isACL(l) {
			t.Errorf("a grant in the main list: %s", l)
		}
	}
}

// The cluster's grants on Postgres's own functions belong to a database: a restore makes
// them in its scratch database (the one renamed into place), never in the app database it
// replaces (clusterSQL connects to that one itself: TestRealMove, 2026-10-28).
func TestClusterSQLForTheScratchDatabase(t *testing.T) {
	s := clusterSQLFor(restoringDatabase)
	if !strings.Contains(s, "\\c "+restoringDatabase+"\n") || strings.Contains(s, "\\c "+AppDatabase+"\n") {
		t.Fatalf("the restore's cluster SQL connects to:\n%s", s)
	}
	if !strings.Contains(s, "pg_drop_replication_slot") || !strings.Contains(s, "pg_reload_conf") {
		t.Fatal("the restore's cluster SQL lost its grants")
	}
}

// What is missing is said by kind, the first names of each, and the rows short.
func TestMissingSaysWhat(t *testing.T) {
	m := &Missing{}
	for i := 0; i < 10; i++ {
		m.add("indexes", "idx"+string(rune('a'+i)))
	}
	m.add("grants", "TABLE videos")
	m.Rows = []string{"public.segment_embeddings: 0 of 5000 rows"}
	s := m.Error()
	for _, want := range []string{"10 indexes (idxa", "and 2 more", "1 grants (TABLE videos)",
		"1 tables without all their rows (public.segment_embeddings: 0 of 5000 rows)"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q lacks %q", s, want)
		}
	}
}

// The vector index leaves the main list for one of its own (built alone, with memory of
// its own: the rollback of 2026-11-07); another index stays in the main one.
func TestRestoreListsSetTheVectorIndexApart(t *testing.T) {
	toc := outageTOC + "4705; 1259 768702 INDEX public segment_embeddings_hnsw maktaba\n" +
		"4706; 1259 768703 INDEX public segment_embeddings_video maktaba\n"
	main, vectors, acls, err := writeRestoreLists(filepath.Join(t.TempDir(), "d.dump"), toc)
	if err != nil {
		t.Fatal(err)
	}
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var kept []string
		for _, l := range strings.Split(string(b), "\n") {
			if l != "" && !strings.HasPrefix(l, ";") {
				kept = append(kept, l)
			}
		}
		return strings.Join(kept, "\n")
	}
	if v := read(vectors); v != "4705; 1259 768702 INDEX public segment_embeddings_hnsw maktaba" {
		t.Fatalf("vector list:\n%s", v)
	}
	m := read(main)
	if strings.Contains(m, "segment_embeddings_hnsw") || !strings.Contains(m, "segment_embeddings_video") {
		t.Fatalf("main list:\n%s", m)
	}
	if strings.Contains(read(acls), "segment_embeddings") {
		t.Fatal("an index in the grants' list")
	}
	if !hasItems(vectors) {
		t.Fatal("the vector list has its index")
	}
	_, none, _, err := writeRestoreLists(filepath.Join(t.TempDir(), "d.dump"), outageTOC)
	if err != nil || hasItems(none) {
		t.Fatalf("a dump without the vector index has an empty vector list (%v)", err)
	}
}

// The vector index is built with a third of the RAM (at most 32 GB) and parallel workers
// first, then without workers, then as the server is set: a rollback never fails on a
// setting meant only to speed it.
func TestIndexBuildsFastestFirstThenTheServersOwn(t *testing.T) {
	got := indexBuilds(96<<30, 28)
	want := []string{
		"-c maintenance_work_mem=32768MB -c max_parallel_maintenance_workers=7",
		"-c maintenance_work_mem=32768MB -c max_parallel_maintenance_workers=0",
		"",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("96 GB, 28 cpus: %q", got)
	}
	if got := indexBuilds(16<<30, 4); got[0] != "-c maintenance_work_mem=5461MB -c max_parallel_maintenance_workers=2" {
		t.Fatalf("16 GB, 4 cpus: %q", got)
	}
	if got := indexBuilds(0, 8); len(got) != 1 || got[0] != "" {
		t.Fatalf("RAM unknown: the server's own settings only, %q", got)
	}
	var tried []string
	var log strings.Builder
	err := buildVectorIndexes(&log, func(opts string) error {
		tried = append(tried, opts)
		if opts != "" {
			return errors.New("could not resize shared memory segment")
		}
		return nil
	})
	if err != nil || tried[len(tried)-1] != "" || !strings.Contains(log.String(), "the vector index is built") {
		t.Fatalf("falls back to the server's settings: err %v, tried %q, log %s", err, tried, log.String())
	}
	if err := buildVectorIndexes(io.Discard, func(string) error { return errors.New("no") }); err == nil {
		t.Fatal("every build failing fails the restore")
	}
}
