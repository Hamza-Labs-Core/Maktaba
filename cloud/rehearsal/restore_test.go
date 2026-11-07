//go:build cloudrehearsal

package rehearsal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// C09 restore-pitr (story 50.09): the pair archives WAL and backs up to two S3 repositories
// (standing in for Hetzner Object Storage and Cloudflare R2) through deploy/cloud-ha's
// pgBackRest config and bin/backup. Rows are written for a while, with repo1 down for part of
// it; then both nodes are destroyed and bin/restore brings the database back on a fresh
// machine to a point T, from repo1 and again from repo2: exactly the rows committed up to T.
// A wrong passphrase reads nothing.
func restorePITR(t *testing.T, _ *cluster) Result {
	extra := &haExtra{
		profiles: []string{"backup"},
		env: []string{"ARCHIVE_COMMAND=pgbackrest --stanza=cloud archive-push %p",
			"REPO1_ENDPOINT=10.60.0.21:9000", "REPO2_ENDPOINT=10.60.0.22:9000"},
	}
	extra.before = func(h *ha) {
		pki := filepath.Join(h.dir, ".work", "s3-pki")
		_ = os.RemoveAll(pki)
		if err := os.MkdirAll(pki, 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes",
			"-keyout", filepath.Join(pki, "private.key"), "-out", filepath.Join(pki, "public.crt"), "-days", "2",
			"-subj", "/CN=s3.rehearsal", "-addext", "subjectAltName=IP:10.60.0.21,IP:10.60.0.22,IP:127.0.0.1").CombinedOutput()
		if err != nil {
			t.Fatalf("s3 cert: %v %s", err, out)
		}
		_ = os.Chmod(filepath.Join(pki, "private.key"), 0o644)
		s3json := `{"identities":[{"name":"rehearsal","credentials":[{"accessKey":"rehearsal","secretKey":"rehearsal-secret"}],"actions":["Admin","Read","Write","List","Tagging"]}]}`
		if err := os.WriteFile(filepath.Join(pki, "s3.json"), []byte(s3json), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := upHAWith(t, "restore-pitr", extra)
	repoPort := map[int]int{1: 29001, 2: 29002}
	s3 := func(method, url string, body ...string) int {
		args := []string{"-sk", "-o", "/dev/null", "-w", "%{http_code}", "--aws-sigv4", "aws:amz:us-east-1:s3",
			"--user", "rehearsal:rehearsal-secret", "-X", method}
		if len(body) > 0 {
			args = append(args, "--data-binary", body[0])
		}
		out, _ := exec.Command("curl", append(args, url)...).Output()
		code := 0
		_, _ = fmt.Sscan(string(out), &code)
		return code
	}
	for r, bucket := range map[int]string{1: "maktaba-prod-pg", 2: "maktaba-prod-pg-r2"} {
		base := fmt.Sprintf("https://127.0.0.1:%d", repoPort[r])
		// SeaweedFS takes a bucket made while its gateway warms up but never serves its
		// objects: wait for the gateway, then make the bucket and prove a round trip
		waitFor(t, time.Minute, fmt.Sprintf("repo%d's S3 gateway", r), func() bool { return s3("GET", base+"/") == 200 })
		time.Sleep(3 * time.Second)
		waitFor(t, time.Minute, fmt.Sprintf("repo%d's bucket", r), func() bool {
			if s3("PUT", base+"/"+bucket) >= 300 {
				return false
			}
			if s3("PUT", base+"/"+bucket+"/probe", "ok") != 200 || s3("GET", base+"/"+bucket+"/probe") != 200 {
				_ = s3("DELETE", base+"/"+bucket+"/probe")
				_ = s3("DELETE", base+"/"+bucket)
				time.Sleep(2 * time.Second)
				return false
			}
			return s3("DELETE", base+"/"+bucket+"/probe") < 300
		})
	}
	leader := h.leader()
	exec1 := func(node string, args ...string) (string, error) {
		out, err := exec.Command("docker", append([]string{"exec", "-u", "postgres", "-e", "PATRONI_URL=http://127.0.0.1:8008",
			"-e", "CLOUD_HA_ENV=/nonexistent", h.project + "-" + node + "-1"}, args...)...).CombinedOutput()
		return string(out), err
	}
	if out, err := exec1(leader, "maktaba-backup", "full"); err != nil {
		t.Fatalf("the first full backup: %v\n%s", err, out)
	}
	if out, _ := exec1(other(leader), "maktaba-backup", "full"); !strings.Contains(out, "not the leader") {
		t.Errorf("a backup ran on the replica: %s", out)
	}

	// writes, with commit times: 40 s, then repo1 down for 20 s, then 20 s more
	db := h.db(leader)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS rehearsal_pitr (id bigint PRIMARY KEY, at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	type row struct {
		id int64
		at time.Time
	}
	var mu sync.Mutex
	var rows []row
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for id := int64(1); ; id++ {
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
			}
			var at time.Time
			if err := db.QueryRow(`INSERT INTO rehearsal_pitr (id) VALUES ($1) RETURNING at`, id).Scan(&at); err == nil {
				mu.Lock()
				rows = append(rows, row{id, at})
				mu.Unlock()
			}
		}
	}()
	time.Sleep(20 * time.Second)
	target := time.Now().UTC()
	time.Sleep(20 * time.Second)
	h.mustHA("stop", "s3-repo1")
	time.Sleep(20 * time.Second)
	h.mustHA("start", "s3-repo1")
	time.Sleep(20 * time.Second)
	close(stop)
	<-done
	// every WAL segment out: switch, then wait for the archiver to catch up
	_, _ = db.Exec(`SELECT pg_switch_wal()`)
	waitFor(t, 3*time.Minute, "the WAL archived", func() bool {
		var failedAfter bool
		var archived int64
		err := db.QueryRow(`SELECT archived_count, coalesce(last_failed_time > last_archived_time, false) FROM pg_stat_archiver`).Scan(&archived, &failedAfter)
		return err == nil && archived > 0 && !failedAfter
	})
	time.Sleep(5 * time.Second)

	mu.Lock()
	var want int
	var lastBefore int64
	for _, r := range rows {
		if !r.at.After(target) {
			want++
			lastBefore = r.id
		}
	}
	total := len(rows)
	mu.Unlock()

	// the pair is gone
	h.mustHA("rm", "-sf", "node-a", "node-b")

	restored := map[int]string{}
	var took time.Duration
	for _, repo := range []int{1, 2} {
		start := time.Now()
		script := fmt.Sprintf(`set -e
/opt/rehearsal-node/pgbr-conf.sh
gosu postgres maktaba-restore --pgdata /tmp/restored --repo %d --target '%s' --port 5499
gosu postgres psql -h /tmp -p 5499 -U postgres -d maktaba -Atqc "SELECT count(*), coalesce(max(id),0), coalesce(min(id),0) FROM rehearsal_pitr"`, repo, target.Format("2006-01-02 15:04:05.000000+00"))
		out, err := h.composeHA("run", "--rm", "--no-deps", "--entrypoint", "bash", "node-a", "-c", script)
		if err != nil {
			t.Fatalf("restore from repo%d: %v\n%s", repo, err, out)
		}
		if repo == 1 {
			took = time.Since(start)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		restored[repo] = lines[len(lines)-1]
	}
	wantLine := fmt.Sprintf("%d|%d|1", want, lastBefore)
	t.Logf("%d rows written, %d up to the target; restored from repo1 %q, repo2 %q (want %q) in %s", total, want, restored[1], restored[2], wantLine, took)
	for repo, got := range restored {
		if got != wantLine {
			t.Errorf("repo%d restored %q, want %q (count|max id|min id up to the target)", repo, got, wantLine)
		}
	}
	// T05: a wrong passphrase reads nothing
	out, err := h.composeHA("run", "--rm", "--no-deps", "--entrypoint", "bash", "node-a", "-c",
		"/opt/rehearsal-node/pgbr-conf.sh && gosu postgres pgbackrest --stanza=cloud --repo=1 --repo1-cipher-pass=wrong info")
	if err == nil && !strings.Contains(out, "error") {
		t.Errorf("pgbackrest info with a wrong passphrase: %v\n%s", err, out)
	}
	// the raw objects are ciphertext
	if raw, err := exec.Command("curl", "-fsS", "-k", "--aws-sigv4", "aws:amz:us-east-1:s3", "--user", "rehearsal:rehearsal-secret",
		fmt.Sprintf("https://127.0.0.1:%d/maktaba-prod-pg/pg/backup/cloud/backup.info", repoPort[1])).Output(); err == nil && strings.Contains(string(raw), "[backup:current]") {
		t.Error("backup.info is readable in the bucket without the passphrase")
	}
	return Result{Notes: []string{fmt.Sprintf("restored %d of %d rows (the ones up to T) from both repositories; the restore took %s (RTO 4 h)", want, total, took)}}
}
