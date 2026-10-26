package tvremote_test

// Epic 34 (first slice) end to end on one machine: the home's streaming
// process (its front door and push relay), a scratch Postgres, real ffmpeg,
// the fake Cloudflare API (cftest) and a fake RTMPS ingest (rtmptest). The
// remote controller and the publisher run in this test process, and a
// stand-in for the site's Worker receives the home's posts. It shows:
//
//   - a public channel is pushed with nobody watching (always on), video
//     stream-copied to the ingest, and its push is held "public";
//   - the home's signed post reaches the site with the channel on air and its
//     live video id; a private channel is not in it;
//   - a token signed with the site's own key for that video plays at the fake
//     Stream (signatures checked), and a tampered one does not; the Worker's
//     TypeScript signer makes byte-identical tokens (cf/parity_test.go);
//   - turning the site off releases the hold.
//
// Runs only with MAKTABA_TEST_PG_URL and ffmpeg on PATH; never in -short.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/rtmptest"
)

func TestPublicEndToEnd(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" || testing.Short() {
		t.Skip("MAKTABA_TEST_PG_URL unset (or -short): skipping the public site end-to-end test")
	}
	rtmptest.NeedFFmpeg(t)
	db, dsn := clustertest.ScratchDB(t)
	ctx := context.Background()

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

	// two channels, one made public; a light software encode on the home
	pubCh, privCh := uuid.NewString(), uuid.NewString()
	for i, id := range []string{pubCh, privCh} {
		if _, err := db.Exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, $2, $3, $4, 'shuffle')`,
			id, 201+i, fmt.Sprintf("Public %d", i+1), fmt.Sprintf("public-%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	lastTest, _ := json.Marshal(map[string]any{"ok": true, "checked_at": time.Now().UTC(), "lines": []any{}})
	if _, err := db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES
		('rules.tv_output', '{"encoder":"software","resolution":"854x480","fps":15,"video_kbps":600,"audio_kbps":64,"segment_seconds":2,"warm_grace_seconds":120}', now()),
		('tv_remote.enabled', 'true', now()), ('tv_remote.last_test', $1, now())`, string(lastTest)); err != nil {
		t.Fatal(err)
	}

	// the fake Cloudflare and its ingest
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
	kid := "e2e-app-key"
	f.Keys[kid] = cf.SigningKey{ID: kid, PEM: signingPEM()}

	store := secrets.NewMemory()
	_ = store.Put(ctx, tvremote.SecretAPIToken, []byte("tok-secret"), "")
	_ = store.Put(ctx, tvremote.SecretSigningKey, []byte(`{"id":"`+kid+`","pem":"`+signingPEM()+`"}`), "")
	in := &tvremote.Integration{DB: db, Secrets: store, NewClient: f.Client,
		Ext: &fakeExt{settings: map[string]any{"account_id": "acc1", "customer_code": "abc123xyz"}}}
	var audits []string
	var amu sync.Mutex
	ctl := &tvremote.Controller{DB: db, Integration: in, TuneWait: 2 * time.Second,
		Audit: func(_ context.Context, action, _, target string, _ map[string]any) {
			amu.Lock()
			audits = append(audits, action+" "+target)
			amu.Unlock()
		}}

	// the site's secrets (the admin's button) and a stand-in for its Worker
	sec, err := ctl.NewSiteSecrets(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Keys[sec.SigningKeyID]; !ok {
		t.Fatal("the site's signing key was not created at Cloudflare")
	}
	var smu sync.Mutex
	var posted []tvremote.SiteState
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get(tvremote.HeaderHomeTime), 10, 64)
		if r.URL.Path != tvremote.SiteStatePath || time.Since(time.Unix(ts, 0)).Abs() > time.Minute ||
			r.Header.Get(tvremote.HeaderHomeSignature) != tvremote.SignHome([]byte(sec.HomeHMAC), r.Method, r.URL.Path, ts, body) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var st tvremote.SiteState
		_ = json.Unmarshal(body, &st)
		smu.Lock()
		posted = append(posted, st)
		smu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer site.Close()

	ptrue := true
	if _, err := ctl.SetPublic(ctx, "admin", pubCh, tvremote.PublicChange{Public: &ptrue, RightsConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	if err := ctl.SavePublicSettings(ctx, "admin", tvremote.PublicSettings{Enabled: true, SiteURL: site.URL}); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go ctl.Run(cctx)
	pub := &tvremote.Publisher{Ctl: ctl, Every: time.Second}
	go pub.Run(cctx)

	// the home
	port := e2eFreePort(t)
	noNode := filepath.Join(t.TempDir(), "no-node.json")
	home := startE2E(t, bin, "home", []string{"DATABASE_URL=" + dsn, "MAKTABA_NODE_CONFIG=" + noNode,
		"MAKTABA_FFMPEG_BIN=ffmpeg", "MAKTABA_FFPROBE_BIN=ffprobe", "MAKTABA_HEALTH_WARM=0s", "MAKTABA_JWKS_URL=",
		"MAKTABA_LOG_LEVEL=info", "MAKTABA_TV_HOST_ROLE=home", "MAKTABA_CLUSTER_ROLE=home",
		fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", port),
		fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_STREAMING_GRPC_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_PUBLIC_BASE_URL=http://127.0.0.1:%d", port),
		"MAKTABA_CACHE_ROOT=" + filepath.Join(t.TempDir(), "home-cache")})

	// 1. always on: the public channel goes live with no viewer at all
	t0 := time.Now()
	var pushID, videoUID string
	e2eWait(t, "the public channel's push to go live", 180*time.Second, func() bool {
		return db.QueryRow(`SELECT id, video_uid FROM tv_remote_pushes WHERE channel_id = $1 AND state = 'live'`,
			pubCh).Scan(&pushID, &videoUID) == nil
	})
	var sessions int
	_ = db.QueryRow(`SELECT count(*) FROM tv_remote_sessions`).Scan(&sessions)
	if sessions != 0 {
		t.Fatalf("%d remote sessions: the public push must need none", sessions)
	}
	var holds string
	_ = db.QueryRow(`SELECT holds::text FROM tv_remote_pushes WHERE id = $1`, pushID).Scan(&holds)
	if !strings.Contains(holds, `"public"`) {
		t.Errorf("holds %s", holds)
	}
	var privPushes int
	_ = db.QueryRow(`SELECT count(*) FROM tv_remote_pushes WHERE channel_id = $1`, privCh).Scan(&privPushes)
	if privPushes != 0 {
		t.Fatalf("the private channel was pushed")
	}
	t.Logf("public channel live with no viewer %v after start (video %s)", time.Since(t0).Round(100*time.Millisecond), videoUID)

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
	e2eWait(t, "8 s of video at the ingest", 60*time.Second, func() bool {
		v := videoTags()
		return len(v) > 0 && v[len(v)-1].TS > 8000
	})
	keys := 0
	for _, tg := range videoTags() {
		if tg.Key {
			keys++
		}
	}
	if keys < 3 {
		t.Errorf("%d keyframes in 8 s of pushed video", keys)
	}
	if hb, _ := os.ReadFile(home.log); !bytes.Contains(hb, []byte("-c:v copy")) {
		t.Error("the relay's command line is not a stream copy")
	}

	// 2. the home's post reaches the site: the public channel on air, its video
	var last tvremote.SiteState
	e2eWait(t, "a post with the channel on air", 30*time.Second, func() bool {
		smu.Lock()
		defer smu.Unlock()
		if len(posted) == 0 {
			return false
		}
		last = posted[len(posted)-1]
		return len(last.Channels) == 1 && last.Channels[0].OnAir
	})
	if last.Channels[0].Slug != "public-1" || last.Channels[0].VideoUID != videoUID || last.CustomerCode != "abc123xyz" {
		t.Fatalf("posted state %+v", last)
	}

	// 3. the site signs a viewer token with its own key for that video: it plays
	raw, err := store.Get(ctx, tvremote.SecretSiteSigningKey)
	if err != nil {
		t.Fatal(err)
	}
	var sk cf.SigningKey
	_ = json.Unmarshal(raw, &sk)
	tok, err := cf.SignToken(sk, last.Channels[0].VideoUID, 10*time.Minute, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cl := f.Client("acc1", "tok-secret")
	get := func(u string) int {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := get(cl.ManifestURL("abc123xyz", tok)); code != 200 {
		t.Fatalf("the site's token does not play: %d", code)
	}
	bad := tok[:len(tok)-6] + "AAAAAA"
	if code := get(cl.ManifestURL("abc123xyz", bad)); code == 200 {
		t.Fatal("a tampered token played")
	}
	t.Logf("site token (kid %s) plays the live video; a tampered one is refused", sec.SigningKeyID)

	// 4. the site switched off: the hold goes, and the push becomes an ordinary
	// one (it ends at the idle timeout, unit-tested in TestPublicPushAlwaysOn)
	if err := ctl.SavePublicSettings(ctx, "admin", tvremote.PublicSettings{Enabled: false, SiteURL: site.URL}); err != nil {
		t.Fatal(err)
	}
	e2eWait(t, "the public hold to go", 30*time.Second, func() bool {
		_ = db.QueryRow(`SELECT holds::text FROM tv_remote_pushes WHERE id = $1`, pushID).Scan(&holds)
		return !strings.Contains(holds, `"public"`)
	})
	amu.Lock()
	defer amu.Unlock()
	if len(audits) == 0 || audits[0] != "tv_public.secrets "+sec.SigningKeyID {
		t.Errorf("audit %v", audits)
	}
}
