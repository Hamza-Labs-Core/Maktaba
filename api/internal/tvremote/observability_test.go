package tvremote_test

// Story 32.11: metrics names and labels (TC1), health samples (TC2), no
// secrets in the logs (TC3), and the Remote panel's status (AC4).

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
)

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (fx *remoteFx) clusterTables() {
	fx.t.Helper()
	fx.exec(`CREATE TABLE IF NOT EXISTS health_samples (at TEXT NOT NULL, host TEXT NOT NULL, metric TEXT NOT NULL,
		value REAL NOT NULL, PRIMARY KEY (metric, host, at))`)
	fx.exec(`CREATE TABLE IF NOT EXISTS streaming_hosts (host_id TEXT PRIMARY KEY, host TEXT NOT NULL, heartbeat_at TEXT NOT NULL)`)
	fx.exec(`CREATE TABLE IF NOT EXISTS channel_runtime (channel_id TEXT PRIMARY KEY, host TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'idle', heartbeat_at TEXT, last_segment_at TEXT)`)
	fx.exec(`CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, username TEXT NOT NULL)`)
}

// failOn makes the next reconcile see an ERR_* code on the channel's push.
func (fx *remoteFx) failOn(ch, code string) {
	fx.t.Helper()
	p := fx.last(ch)
	uid := fx.cfInput(p.ID)
	fx.relay(p.ID, "connecting")
	fx.f.Mu.Lock()
	fx.f.Status = map[string]json.RawMessage{uid: json.RawMessage(`{"current":{"state":"errored","reason":"` + code + `"}}`)}
	fx.f.Mu.Unlock()
	fx.reconcile()
}

func TestMetricsRegistered(t *testing.T) {
	fx := newRemote(t)
	fx.clusterTables()
	fx.liveTune("u1", fx.chans[0])
	hs := &tvremote.HealthSampler{DB: fx.db, Remote: fx.c, Host: "mac", Now: fx.clock}
	if err := hs.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _ = fx.tune("u2", fx.chans[1], "")
	fx.failOn(fx.chans[1], "ERR_GOP_OUT_OF_RANGE")

	fams, err := metrics.Reg().Gather()
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string][]string{}
	values := map[string][]*dto.Metric{}
	for _, f := range fams {
		for _, m := range f.GetMetric() {
			var ls []string
			for _, l := range m.GetLabel() {
				ls = append(ls, l.GetName())
			}
			labels[f.GetName()] = ls
			values[f.GetName()] = append(values[f.GetName()], m)
		}
	}
	want := map[string]string{
		tvremote.MetricRemoteViewers:    "channel",
		tvremote.MetricRemotePushes:     "state",
		tvremote.MetricRemotePushErrors: "code",
		tvremote.MetricRemoteMinutes:    "month",
		tvremote.MetricRemoteCost:       "month",
	}
	for name, label := range want {
		ls, ok := labels[name]
		if !ok {
			t.Errorf("%s not exported", name)
			continue
		}
		if len(ls) != 1 || ls[0] != label {
			t.Errorf("%s labels %v, want [%s]", name, ls, label)
		}
	}
	found := false
	for _, m := range values[tvremote.MetricRemotePushErrors] {
		if m.GetLabel()[0].GetValue() == "ERR_GOP_OUT_OF_RANGE" && m.GetCounter().GetValue() >= 1 {
			found = true
		}
	}
	if !found {
		t.Error("the retried push was not counted under its ERR_* code")
	}
	for _, m := range values[tvremote.MetricRemoteViewers] {
		if v := m.GetLabel()[0].GetValue(); v != fx.chans[0] {
			t.Errorf("viewers label %q is not the channel id", v)
		}
	}
	live := false
	for _, m := range values[tvremote.MetricRemotePushes] {
		if m.GetLabel()[0].GetValue() == tvremote.PushLive && m.GetGauge().GetValue() == 1 {
			live = true
		}
	}
	if !live {
		t.Error("the live push is not reported")
	}
}

func TestHealthSamplesWritten(t *testing.T) {
	fx := newRemote(t)
	fx.clusterTables()
	now := fx.clock()
	fresh := now.Add(-5 * time.Second).Format(time.RFC3339)
	old := now.Add(-10 * time.Minute).Format(time.RFC3339)
	fx.exec(`INSERT INTO streaming_hosts (host_id, host, heartbeat_at) VALUES ('h1','mac',$1), ('h2','pc',$2)`, fresh, old)
	fx.exec(`INSERT INTO channel_runtime (channel_id, host, state, heartbeat_at) VALUES ($1,'mac','running',$2), ($3,'mac','idle',$2), ($4,'pc','running',$5)`,
		fx.chans[0], fresh, fx.chans[1], fx.chans[2], old)
	fx.liveTune("u1", fx.chans[0])
	hs := &tvremote.HealthSampler{DB: fx.db, Remote: fx.c, Host: "mac", Now: fx.clock}
	if err := hs.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	fx.advance(time.Minute)
	beat := fx.clock().Add(-5 * time.Second).Format(time.RFC3339)
	fx.exec(`UPDATE streaming_hosts SET heartbeat_at = $1 WHERE host_id = 'h1'`, beat)
	fx.exec(`UPDATE channel_runtime SET heartbeat_at = $1 WHERE host = 'mac'`, beat)
	if err := hs.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := fx.db.Query(`SELECT at, host, metric, value FROM health_samples ORDER BY at, metric`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]float64{}
	ats := map[string]bool{}
	for rows.Next() {
		var at, host, metric string
		var v float64
		if err := rows.Scan(&at, &host, &metric, &v); err != nil {
			t.Fatal(err)
		}
		if host != "mac" {
			t.Errorf("host %q", host)
		}
		if _, err := time.Parse("2006-01-02T15:04:05.000Z", at); err != nil {
			t.Errorf("at %q is not the health_samples layout", at)
		}
		ats[at] = true
		got[metric] = v
	}
	if len(ats) != 2 {
		t.Errorf("want one sample per minute, got minutes %v", ats)
	}
	want := map[string]float64{
		tvremote.SampleHostsUp: 1, tvremote.SampleChannels: 1, tvremote.SampleViewers: 1,
		tvremote.SamplePushesLive: 1,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v (all %v)", k, got[k], v, got)
		}
	}
	if _, ok := got[tvremote.SampleRemoteCost]; !ok {
		t.Error("no cost sample")
	}
}

func TestNoSecretsInLogs(t *testing.T) {
	fx := newRemote(t)
	fx.clusterTables()
	buf := &syncBuf{}
	fx.c.Logger = slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fx.in.Logger = fx.c.Logger
	r := fx.liveTune("u1", fx.chans[0])
	ctx := context.Background()
	fx.advance(40 * time.Minute)
	b, err := fx.c.Heartbeat(ctx, viewer("u1"), r.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.c.RotateSigningKey(ctx, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := fx.c.End(ctx, viewer("u1"), r.SessionID); err != nil {
		t.Fatal(err)
	}
	// a failing push on another channel
	_, _ = fx.tune("u2", fx.chans[1], "")
	fx.failOn(fx.chans[1], "ERR_UNHEALTHY")
	fx.advance(10 * time.Minute)
	fx.reconcile()

	logs := buf.String()
	if !strings.Contains(logs, `"event":"tv_push_start"`) || !strings.Contains(logs, `"event":"tv_push_error"`) {
		t.Fatalf("the push events were not logged:\n%s", logs)
	}
	secrets := []string{"tok-secret", tokenOf(r.HLSURL), b.Token}
	for _, in := range fx.f.Inputs {
		secrets = append(secrets, in.RTMPS.StreamKey)
		if in.SRT.Passphrase != "" {
			secrets = append(secrets, in.SRT.Passphrase)
		}
	}
	for _, s := range secrets {
		if len(s) >= 8 && strings.Contains(logs, s) {
			t.Errorf("a secret (%s…) is in the logs", s[:6])
		}
	}
	if strings.Contains(logs, "PRIVATE KEY") || strings.Contains(logs, signingPEM()[:40]) {
		t.Error("the signing key is in the logs")
	}
}

func TestStatusSessionsEventsAndViews(t *testing.T) {
	fx := newRemote(t)
	fx.clusterTables()
	fx.exec(`INSERT INTO users (id, username) VALUES ('u1', 'amina')`)
	buf := &syncBuf{}
	fx.c.Logger = slog.New(slog.NewJSONHandler(buf, nil))
	fx.c.EventSource = func() []json.RawMessage {
		var out []json.RawMessage
		for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			out = append(out, json.RawMessage(l))
		}
		return out
	}
	fx.liveTune("u1", fx.chans[0])
	p := fx.last(fx.chans[0])
	fx.f.Mu.Lock()
	fx.f.Views = map[string]int{fx.cfInput(p.ID): 3}
	fx.f.Mu.Unlock()
	st, err := fx.c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.SessionList) != 1 || st.SessionList[0].User != "amina" || st.SessionList[0].Device != "phone" ||
		st.SessionList[0].ChannelNumber != 101 {
		t.Fatalf("sessions %+v", st.SessionList)
	}
	if len(st.Pushes) != 1 || st.Pushes[0].CFViewers == nil || *st.Pushes[0].CFViewers != 3 || st.Pushes[0].Viewers != 1 {
		t.Fatalf("pushes %+v", st.Pushes)
	}
	if len(st.Events) == 0 {
		t.Fatal("no events")
	}
	for _, e := range st.Events {
		if !strings.HasPrefix(e.Event, "tv_") {
			t.Errorf("not a remote event: %+v", e)
		}
	}
	// a hidden count: nil (shown as —)
	fx.f.Mu.Lock()
	fx.f.Views = nil
	fx.f.Mu.Unlock()
	st, _ = fx.c.Status(context.Background())
	if st.Pushes[0].CFViewers != nil {
		t.Errorf("hidden count: %v", *st.Pushes[0].CFViewers)
	}
}

func TestParseEventsKeepsRemoteOnly(t *testing.T) {
	lines := []json.RawMessage{
		json.RawMessage(`{"ts":"2026-10-19T12:00:00Z","level":"INFO","msg":"x","event":"tv_push_live","push_id":"p","channel_id":"c"}`),
		json.RawMessage(`{"level":"INFO","msg":"y","event":"p27_mounted"}`),
		json.RawMessage(`not json`),
		json.RawMessage(`{"level":"WARN","msg":"z","event":"tv_budget_80"}`),
	}
	ev := tvremote.ParseEvents(lines, 20)
	if len(ev) != 2 || ev[0].PushID != "p" || ev[0].At.IsZero() || ev[1].Event != "tv_budget_80" {
		t.Fatalf("%+v", ev)
	}
	if got := tvremote.ParseEvents(lines, 1); len(got) != 1 || got[0].Event != "tv_budget_80" {
		t.Fatalf("limit: %+v", got)
	}
	if tvremote.ErrCode("Cloudflare: the input was unhealthy (ERR_UNHEALTHY)", "x") != "ERR_UNHEALTHY" ||
		tvremote.ErrCode("relay gone", "push_failed") != "push_failed" {
		t.Error("ErrCode")
	}
}
