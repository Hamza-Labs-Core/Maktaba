package tvremote_test

// Epic 32 stories 32.7, 32.8 and 32.10 end to end on one machine: two real
// streaming processes (the home with its front door and push relay, and a
// node), a scratch Postgres, real ffmpeg, the fake Cloudflare API (cftest)
// and a fake RTMPS ingest (rtmptest) standing in for Cloudflare's. The remote
// controller and the viewer API run in this test process. It shows:
//
//   - a remote viewer tuning through the API gets a signed HLS URL whose
//     token verifies against the signing key (and plays at the fake);
//   - the channel encoded on the node is pushed by the home's relay, video
//     stream-copied (no second encode);
//   - one continuous push session per channel: the ingest sees one publisher
//     connection for the whole test, across a node failover (the relay
//     switches its source to the home), with monotonic timestamps and no gap
//     beyond a fraction of a second;
//   - the per-user limit (429), the budget refusal (402) with the running
//     session kept, and the storage guard (503);
//   - stopping the push disconnects the ingest and deletes the recording.
//
// Runs only with MAKTABA_TEST_PG_URL and ffmpeg on PATH; never in -short.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	tvremoteh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/rtmptest"
)

func e2eFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type e2eProc struct {
	cmd *exec.Cmd
	log string
}

func (p *e2eProc) kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill() // only the PID this test started
		_, _ = p.cmd.Process.Wait()
	}
}

func startE2E(t *testing.T, bin, name string, env []string) *e2eProc {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), name+".log")
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "serve")
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &e2eProc{cmd: cmd, log: logPath}
	t.Cleanup(func() {
		p.kill()
		_ = lf.Close()
		if t.Failed() {
			b, _ := os.ReadFile(logPath)
			lines := strings.Split(string(b), "\n")
			if len(lines) > 80 {
				lines = lines[len(lines)-80:]
			}
			t.Logf("--- %s log (tail) ---\n%s", name, strings.Join(lines, "\n"))
		}
	})
	return p
}

func e2eWait(t *testing.T, what string, d time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRemoteEndToEnd(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" || testing.Short() {
		t.Skip("MAKTABA_TEST_PG_URL unset (or -short): skipping the remote end-to-end test")
	}
	rtmptest.NeedFFmpeg(t)
	db, dsn := clustertest.ScratchDB(t)
	ctx := context.Background()

	// the streaming binary
	_, file, _, _ := runtime.Caller(0)
	streamingDir := filepath.Join(filepath.Dir(file), "..", "..", "..", "streaming")
	bin := filepath.Join(t.TempDir(), "maktaba-streaming")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = streamingDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build streaming: %v\n%s", err, out)
	}

	// two channels; a light software encode; machines may stream
	ch1, ch2 := uuid.NewString(), uuid.NewString()
	for i, id := range []string{ch1, ch2} {
		if _, err := db.Exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, $2, $3, $4, 'shuffle')`,
			id, 105+i, fmt.Sprintf("Remote %d", i+1), fmt.Sprintf("remote-%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES
		('rules.tv_output', '{"encoder":"software","resolution":"854x480","fps":15,"video_kbps":600,"audio_kbps":64,"segment_seconds":2,"warm_grace_seconds":120}', now()),
		('rules.tv_cluster', '{"nodes_stream":true,"dead_after_seconds":15}', now()),
		('tv_remote.enabled', 'true', now())`); err != nil {
		t.Fatal(err)
	}

	// the fake Cloudflare and its ingest: a publisher makes the input live
	f := cftest.New(t)
	f.VerifySignatures = true
	ing, err := rtmptest.Listen(true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ing.Close)
	f.IngestRTMPS = ing.URL()
	ing.OnPublish = func(key string) {
		if uid, ok := f.InputByStreamKey(key); ok {
			f.GoLive(uid)
		}
	}
	ing.OnClose = func(key string) {
		if uid, ok := f.InputByStreamKey(key); ok {
			f.EndLive(uid)
		}
	}
	kid := "e2e-key"
	f.Keys[kid] = cf.SigningKey{ID: kid, PEM: signingPEM()}

	// the home's remote controller and viewer API (in this process)
	store := secrets.NewMemory()
	_ = store.Put(ctx, tvremote.SecretAPIToken, []byte("tok-secret"), "")
	_ = store.Put(ctx, tvremote.SecretSigningKey, []byte(`{"id":"`+kid+`","pem":"`+signingPEM()+`"}`), "")
	in := &tvremote.Integration{DB: db, Secrets: store, NewClient: f.Client,
		Ext: &fakeExt{settings: map[string]any{"account_id": "acc1", "customer_code": "abc123xyz"}}}
	ctl := &tvremote.Controller{DB: db, Integration: in, TuneWait: 2 * time.Second}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go ctl.Run(cctx)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if u := req.Header.Get("X-Test-User"); u != "" {
				req = req.WithContext(principal.WithPrincipal(req.Context(),
					&principal.Principal{UserID: u, IsAdmin: u == "admin", AccessAllLibraries: true}))
			}
			next.ServeHTTP(w, req)
		})
	})
	(&tvremoteh.Handler{Integration: in, Remote: ctl}).Mount(r)
	api := httptest.NewServer(r)
	defer api.Close()
	call := func(user, method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-User", user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	tune := func(user, ch, device string) (int, map[string]any) {
		return call(user, "POST", "/api/tv/remote/tune", fmt.Sprintf(`{"channel_id":%q,"device":%q}`, ch, device))
	}

	// the home and a node
	homePort, nodePort := e2eFreePort(t), e2eFreePort(t)
	nodeID := uuid.NewString()
	noNode := filepath.Join(t.TempDir(), "no-node.json") // never this machine's real node config
	common := []string{"DATABASE_URL=" + dsn, "MAKTABA_NODE_CONFIG=" + noNode, "MAKTABA_FFMPEG_BIN=ffmpeg",
		"MAKTABA_FFPROBE_BIN=ffprobe", "MAKTABA_HEALTH_WARM=0s", "MAKTABA_JWKS_URL=", "MAKTABA_LOG_LEVEL=info"}
	home := startE2E(t, bin, "home", append(common,
		"MAKTABA_TV_HOST_ROLE=home", "MAKTABA_CLUSTER_ROLE=home",
		fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", homePort),
		fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_STREAMING_GRPC_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_PUBLIC_BASE_URL=http://127.0.0.1:%d", homePort),
		"MAKTABA_CACHE_ROOT="+filepath.Join(t.TempDir(), "home-cache")))
	node := startE2E(t, bin, "node", append(common,
		"MAKTABA_TV_HOST_ROLE=node", "MAKTABA_NODE_ID="+nodeID,
		fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", nodePort),
		fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_PUBLIC_BASE_URL=http://127.0.0.1:%d", nodePort),
		"MAKTABA_CACHE_ROOT="+filepath.Join(t.TempDir(), "node-cache")))
	var homeID string
	e2eWait(t, "both heartbeats", 90*time.Second, func() bool {
		var n int
		_ = db.QueryRow(`SELECT count(*) FROM streaming_hosts WHERE heartbeat_at > now() - interval '10 seconds'`).Scan(&n)
		return n == 2
	})
	if err := db.QueryRow(`SELECT host_id FROM streaming_hosts WHERE role = 'home'`).Scan(&homeID); err != nil {
		t.Fatal(err)
	}
	// the node is switched on; the home is full, so channels encode on the node
	if _, err := db.Exec(`UPDATE streaming_hosts SET enabled = (host_id = $1) WHERE host_id = $1`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE streaming_hosts SET capacity_override = 0 WHERE host_id = $1`, homeID); err != nil {
		t.Fatal(err)
	}
	e2eWait(t, "the home's push relay", 60*time.Second, func() bool {
		b, _ := os.ReadFile(home.log)
		return bytes.Contains(b, []byte("tv_push_relay_ready"))
	})
	time.Sleep(6 * time.Second) // one beat + one host refresh

	// 1. a remote viewer tunes channel 105 through the API
	t0 := time.Now()
	var tuned map[string]any
	e2eWait(t, "the tune to answer 200", 120*time.Second, func() bool {
		code, m := tune("amal", ch1, "phone")
		if code != 200 && code != 202 {
			t.Fatalf("tune: %d %v", code, m)
		}
		tuned = m
		return code == 200
	})
	t.Logf("tune → 200 after %v", time.Since(t0).Round(100*time.Millisecond))
	hls, _ := tuned["hls_url"].(string)
	tok := tokenOf(hls)
	pub, _ := f.PublicKey(kid)
	if err := cftest.VerifyToken(tok, pub); err != nil {
		t.Fatalf("the playback token does not verify against the signing key: %v", err)
	}
	c, _ := cfClaims(tok)
	var pushID, videoUID, inputUID string
	if err := db.QueryRow(`SELECT p.id, p.video_uid, i.cf_uid FROM tv_remote_pushes p JOIN tv_remote_inputs i ON i.id = p.input_id
		WHERE p.channel_id = $1 AND p.state = 'live'`, ch1).Scan(&pushID, &videoUID, &inputUID); err != nil {
		t.Fatal(err)
	}
	if c.Sub != videoUID || c.Kid != kid {
		t.Fatalf("claims %+v, video %s", c, videoUID)
	}
	resp, err := http.Get(hls)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the signed URL does not play at the fake: %v %v", resp, err)
	}
	resp.Body.Close()
	var placedOn string
	_ = db.QueryRow(`SELECT host_id FROM channel_placements WHERE channel_id = $1 AND state <> 'released'`, ch1).Scan(&placedOn)
	if placedOn != nodeID {
		t.Fatalf("channel encoded on %q, want the node", placedOn)
	}
	t.Logf("token verifies (sub = live video %s, kid %s); channel encoded on the node, pushed by the home", videoUID, kid)

	key := ing.Publishes()[0].Key
	videoTags := func() []rtmptest.Tag {
		var out []rtmptest.Tag
		for _, p := range ing.Publishes() {
			for _, tg := range p.Tags {
				if tg.Type == 9 && !tg.SeqHeader {
					out = append(out, tg)
				}
			}
		}
		return out
	}
	e2eWait(t, "5 s of video at the ingest", 30*time.Second, func() bool {
		v := videoTags()
		return len(v) > 0 && v[len(v)-1].TS > 5000
	})

	// 2. the per-user limit: two streams (two devices on one channel count twice)
	if code, m := tune("amal", ch1, "tablet"); code != 200 {
		t.Fatalf("second device: %d %v", code, m)
	}
	if code, m := tune("amal", ch1, "tv"); code != 429 || m["code"] != "user_streams_full" {
		t.Fatalf("third stream: %d %v", code, m)
	}
	var pushes int
	_ = db.QueryRow(`SELECT count(*) FROM tv_remote_pushes WHERE channel_id = $1`, ch1).Scan(&pushes)
	if pushes != 1 {
		t.Fatalf("pushes of the channel = %d, want one", pushes)
	}
	t.Logf("per-user limit: third stream → 429 user_streams_full; still one push for the channel")

	// 3. the budget at 100 %: new tunes 402, the running session keeps going
	setRules := func(v string) {
		if _, err := db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES ('rules.tv_remote', $1, now())
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, v); err != nil {
			t.Fatal(err)
		}
	}
	setRules(`{"monthly_budget_usd": 4}`)
	if code, m := tune("badr", ch1, "phone"); code != 402 || m["code"] != "budget_exhausted" {
		t.Fatalf("over budget: %d %v", code, m)
	}
	if code, m := call("amal", "POST", "/api/tv/remote/sessions/"+tuned["session_id"].(string)+"/heartbeat", ""); code != 200 || m["state"] != "live" {
		t.Fatalf("the running session's heartbeat at 100 %%: %d %v", code, m)
	}
	setRules(`{}`)
	t.Logf("budget: new tune → 402 budget_exhausted; the running session's heartbeat → 200 live")

	// 4. the storage guard: a 900-minute account has room for one 8 h broadcast only
	f.Mu.Lock()
	f.StorageLimit = 900
	f.Mu.Unlock()
	ctl.ForgetStorage()
	if code, m := tune("badr", ch2, "phone"); code != 503 || m["code"] != "remote_storage_full" {
		t.Fatalf("storage guard: %d %v", code, m)
	}
	f.Mu.Lock()
	f.StorageLimit = 1000
	f.Mu.Unlock()
	ctl.ForgetStorage()
	t.Logf("storage guard: a second broadcast on 900 minutes → 503 remote_storage_full")

	// 5. failover: the node dies; the channel continues on the home; the relay
	// switches its source and keeps the one ingest session
	if _, err := db.Exec(`UPDATE streaming_hosts SET capacity_override = 2 WHERE host_id = $1`, homeID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	before := len(videoTags())
	killedAt := time.Now()
	node.kill()
	e2eWait(t, "video from the home after the node died", 60*time.Second, func() bool {
		v := videoTags()
		return len(v) > before+45 && v[len(v)-1].At.After(killedAt.Add(3*time.Second))
	})
	var after string
	_ = db.QueryRow(`SELECT host_id FROM channel_placements WHERE channel_id = $1 AND state <> 'released'`, ch1).Scan(&after)
	if after != homeID {
		t.Fatalf("after the failover the channel is on %q, want the home", after)
	}
	pubs := ing.Publishes()
	if len(pubs) != 1 || !pubs[0].End.IsZero() || ing.Active(key) != 1 {
		t.Fatalf("ingest sessions: %d (want one, still connected)", len(pubs))
	}
	v := videoTags()
	var maxGap uint32
	for i := 1; i < len(v); i++ {
		if v[i].TS < v[i-1].TS {
			t.Fatalf("video timestamp went back at the ingest: %d after %d", v[i].TS, v[i-1].TS)
		}
		if g := v[i].TS - v[i-1].TS; g > maxGap {
			maxGap = g
		}
	}
	if maxGap > 1000 {
		t.Errorf("a %d ms gap in the pushed timestamps", maxGap)
	}
	var sameVideo string
	_ = db.QueryRow(`SELECT video_uid FROM tv_remote_pushes WHERE id = $1`, pushID).Scan(&sameVideo)
	if sameVideo != videoUID {
		t.Errorf("the broadcast changed across the failover: %s → %s", videoUID, sameVideo)
	}
	hb, _ := os.ReadFile(home.log)
	if !bytes.Contains(hb, []byte("tv_push_source_switch")) {
		t.Error("the home's relay logged no source switch")
	}
	if !bytes.Contains(hb, []byte("-c:v copy")) || bytes.Contains(hb, []byte(strings.TrimPrefix(key, "/"))) {
		t.Error("the relay's command line is not a stream copy, or the stream key is in the log")
	}
	t.Logf("failover: one ingest connection, %d video tags, monotonic, max gap %d ms; same video id; data again %v after the kill",
		len(v), maxGap, v[len(v)-1].At.Sub(killedAt).Round(time.Second))

	// 6. stop: the ingest disconnects, the input is disabled, the recording is deleted
	if code, _ := call("admin", "POST", "/api/tv/remote/pushes/"+pushID+"/stop", ""); code != 202 {
		t.Fatalf("stop: %d", code)
	}
	e2eWait(t, "the push to finish", 60*time.Second, func() bool {
		var st string
		_ = db.QueryRow(`SELECT state FROM tv_remote_pushes WHERE id = $1`, pushID).Scan(&st)
		return st == tvremote.PushDone
	})
	f.Mu.Lock()
	_, kept := f.Videos[videoUID]
	f.Mu.Unlock()
	if kept || f.Enabled(inputUID) || ing.Active(key) != 0 {
		t.Fatalf("after stop: recording kept %v, input enabled %v, publishers %d", kept, f.Enabled(inputUID), ing.Active(key))
	}
	t.Logf("stop: ingest disconnected, input disabled, recording deleted")
}
