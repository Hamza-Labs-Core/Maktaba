package liveguard

// Review findings 1 and 11 at the stream: live TV is refused, cut and counted by the
// time rules, and the verdict is asked for the sign-in the stream's credential names.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
)

const liveTSPath = "/stream/channel/11111111-1111-4111-8111-111111111111/live.ts"

func restrictedAllow(uuid.UUID, uuid.UUID) (Verdict, error) {
	return Verdict{Restricted: true, ChannelAllowed: true, Allowed: true}, nil
}

// served sums what the guard told the API it served of channel.
func servedOf(qs []Question, channel uuid.UUID) time.Duration {
	var d time.Duration
	for _, q := range qs {
		if q.Channel == channel {
			d += q.Served
		}
	}
	return d
}

// A viewer whose time is over is refused the channel with the parental-time problem
// (reason, next_allowed_at), not the slate of a programme above the limit, and
// nothing is streamed; when the time comes back, so does the channel.
func TestTimeOverRefusesTheChannel(t *testing.T) {
	f := newFixture(t)
	next := t0.Add(9 * time.Hour)
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) {
		// the programme is above the limit as well: the time's answer comes first
		return Verdict{Restricted: true, ChannelAllowed: true, Allowed: false,
			Time: &TimeRefusal{Reason: "outside_slot", NextAllowedAt: &next}}, nil
	})
	path := chanPath + "?sig=" + f.token(t, nil)
	w := f.get(path, stranger, nil)
	var body struct {
		Type, Code, Reason string
		NextAllowedAt      string `json:"next_allowed_at"`
		Message            map[string]string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 403 || !strings.HasSuffix(body.Type, "/parental-time") {
		t.Fatalf("time over = %d %s", w.Code, w.Body.String())
	}
	if body.Code != "parental_time" || body.Reason != "outside_slot" || body.NextAllowedAt != next.Format(time.RFC3339) ||
		body.Message["en"] != TimeOverEN || body.Message["ar"] != TimeOverAR {
		t.Fatalf("refusal = %+v", body)
	}
	if f.served != 0 {
		t.Fatal("a refused channel was streamed")
	}
	// a hidden channel says nothing about the time
	f.clk.add(VerdictTTL)
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) {
		return Verdict{Restricted: true, Time: &TimeRefusal{Reason: "limit_reached"}}, nil
	})
	if w := f.get(path, stranger, nil); w.Code != 404 {
		t.Fatalf("a hidden channel with the time over = %d, want 404", w.Code)
	}
	f.clk.add(VerdictTTL)
	f.v.set(restrictedAllow)
	if w := f.get(path, stranger, nil); w.Code != 200 {
		t.Fatalf("after the time came back = %d", w.Code)
	}
}

// The verdict is asked again when the time rules end the stream: an HLS viewer is
// refused its next segment at that moment, not VerdictTTL later.
func TestTimeEndCutsTheVerdictShort(t *testing.T) {
	f := newFixture(t)
	end := t0.Add(4 * time.Second)
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) {
		return Verdict{Restricted: true, ChannelAllowed: true, Allowed: true, TimeEndsAt: &end}, nil
	})
	path := chanPath + "?sig=" + f.token(t, nil)
	if w := f.get(path, stranger, nil); w.Code != 200 {
		t.Fatalf("inside the time = %d", w.Code)
	}
	f.clk.add(3 * time.Second)
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) {
		return Verdict{Restricted: true, ChannelAllowed: true, Allowed: true, Time: &TimeRefusal{Reason: "limit_reached"}}, nil
	})
	if w := f.get(path, stranger, nil); w.Code != 200 {
		t.Fatalf("one second before the end = %d", w.Code)
	}
	f.clk.add(time.Second)
	if w := f.get(path, stranger, nil); w.Code != 403 {
		t.Fatalf("at the end of the time = %d, want 403", w.Code)
	}
}

// What the guard serves a restricted viewer is counted from the stream and said to
// the API with its questions: the time between served requests, a channel left for
// another, nothing while the viewer is away, and nothing lost when the API fails.
func TestServedTimeIsReported(t *testing.T) {
	f := newFixture(t)
	f.v.set(restrictedAllow)
	path := chanPath + "?sig=" + f.token(t, nil)
	get := func(want int) {
		t.Helper()
		if w := f.get(path, stranger, nil); w.Code != want {
			t.Fatalf("at +%s: %d, want %d", f.clk.now().Sub(t0), w.Code, want)
		}
	}
	get(200)                  // the first question: nothing served yet
	for i := 0; i < 10; i++ { // a segment every 6 s for a minute
		f.clk.add(6 * time.Second)
		get(200)
	}
	qs := f.v.questions()
	if got := servedOf(qs, testChannel); got < 45*time.Second || got > 60*time.Second {
		t.Fatalf("a minute of segments: %s reported in %d questions", got, len(qs))
	}
	if qs[0].Served != 0 {
		t.Fatalf("the first question reported %s", qs[0].Served)
	}

	// the API fails: the request is refused and what was owed is said again
	f.clk.add(VerdictTTL)
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) { return Verdict{}, errors.New("down") })
	get(503)
	f.v.set(restrictedAllow)
	get(200)
	if got := servedOf(f.v.questions(), testChannel); got < 60*time.Second {
		t.Fatalf("after a failed question the report is %s: time was lost", got)
	}

	// the viewer changes channel: the next question, for the other channel, also
	// says what was served of the one it left
	f.clk.add(6 * time.Second)
	get(200)
	before := servedOf(f.v.questions(), testChannel)
	other := strings.Replace(chanPath, testChannel.String(), otherChan.String(), 1) + "?sig=" +
		f.token(t, func(c *auth.Claims) { c.Sub = otherChan.String() })
	f.clk.add(2 * time.Second)
	if w := f.get(other, stranger, nil); w.Code != 200 {
		t.Fatalf("the other channel = %d", w.Code)
	}
	if got := servedOf(f.v.questions(), testChannel) - before; got < 6*time.Second {
		t.Fatalf("leaving a channel reported %s of it, want the seconds served since its last question", got)
	}

	// away for ten minutes: the silence is not counted
	total := servedOf(f.v.questions(), testChannel)
	f.clk.add(10 * time.Minute)
	get(200)
	f.clk.add(VerdictTTL)
	get(200)
	if got := servedOf(f.v.questions(), testChannel) - total; got > VerdictTTL {
		t.Fatalf("ten minutes away were counted: %s", got)
	}
}

// A viewer with no limit is not counted, and neither is what the admin token or a
// tuner address (Plex) is served: they name no account.
func TestUnrestrictedAndTunerAreNotCounted(t *testing.T) {
	f := newFixture(t)
	path := chanPath + "?sig=" + f.token(t, nil)
	for i := 0; i < 8; i++ {
		if w := f.get(path, stranger, nil); w.Code != 200 {
			t.Fatalf("a viewer with no limit = %d", w.Code)
		}
		if w := f.get(chanPath, plexAddr, nil); w.Code != 200 {
			t.Fatalf("Plex = %d", w.Code)
		}
		f.clk.add(6 * time.Second)
	}
	for _, q := range f.v.questions() {
		if q.Served != 0 {
			t.Fatalf("reported %s for a viewer with no limit", q.Served)
		}
	}
	// the time being over for an account stops no tuner: no one is asked
	asked := f.v.asked()
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) {
		return Verdict{Restricted: true, ChannelAllowed: true, Allowed: true, Time: &TimeRefusal{Reason: "limit_reached"}}, nil
	})
	if w := f.get(chanPath, plexAddr, nil); w.Code != 200 || f.v.asked() != asked {
		t.Fatalf("Plex with a viewer's time over = %d, %d new questions", w.Code, f.v.asked()-asked)
	}
}

// A continuous stream is cut when the time is over, and what it wrote is counted.
func TestLiveTSIsCutAndCountedByTime(t *testing.T) {
	f := newFixture(t)
	tick := make(chan time.Time)
	waits := make(chan time.Duration, 8)
	f.g.After = func(d time.Duration) <-chan time.Time { waits <- d; return tick }
	end := t0.Add(10 * time.Second)
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) {
		return Verdict{Restricted: true, ChannelAllowed: true, Allowed: true, TimeEndsAt: &end}, nil
	})
	step := make(chan struct{})
	ended := make(chan error, 1)
	h := f.g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for {
			if _, err := w.Write([]byte("ts")); err != nil {
				ended <- err
				return
			}
			select {
			case <-r.Context().Done():
				_, err := w.Write([]byte("more"))
				ended <- err
				return
			case <-step:
			}
		}
	}))
	r := httptest.NewRequest(http.MethodGet, liveTSPath+"?sig="+f.token(t, nil), nil)
	r.RemoteAddr = stranger
	done := make(chan struct{})
	go func() { h.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	if d := <-waits; d != 10*time.Second {
		t.Fatalf("the first check waits %s, want the 10 s to the end of the time", d)
	}
	for i := 0; i < 10; i++ { // ten seconds of stream, a write a second
		f.clk.add(time.Second)
		step <- struct{}{}
	}
	f.v.set(func(uuid.UUID, uuid.UUID) (Verdict, error) {
		return Verdict{Restricted: true, ChannelAllowed: true, Allowed: true, Time: &TimeRefusal{Reason: "limit_reached"}}, nil
	})
	tick <- f.clk.now()
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("a write after the time was over succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream was not cut when the time was over")
	}
	<-done
	if got := servedOf(f.v.questions(), testChannel); got < 8*time.Second || got > 10*time.Second {
		t.Fatalf("ten seconds of live.ts: %s reported", got)
	}
}

// Finding 11: the verdict is asked for the sign-in the credential names (a signed
// URL's fam and ac, or the web port gate's headers), and one sign-in's answer is
// never reused for another. An account with no limit on a child's device is cut when
// a programme above the device's limit starts, while its other sign-in plays on.
func TestVerdictIsAskedForTheSignIn(t *testing.T) {
	f := newFixture(t)
	const family = "0f0f0f0f-1111-4222-8333-444444444444"
	nine := 9
	// the API's answer by sign-in: the child's device is limited to 9+, and a 16
	// programme is on
	f.v.mu.Lock()
	f.v.byQuestion = func(q Question) (Verdict, error) {
		if q.Family == family || (q.Ceiling != nil && *q.Ceiling <= 9) {
			return Verdict{Restricted: true, ChannelAllowed: true, Allowed: false}, nil
		}
		return Verdict{ChannelAllowed: true, Allowed: true}, nil
	}
	f.v.mu.Unlock()

	plain := chanPath + "?sig=" + f.token(t, nil)
	device := chanPath + "?sig=" + f.token(t, func(c *auth.Claims) { c.Fam, c.Ac = family, &nine })
	if w := f.get(plain, stranger, nil); w.Code != 200 {
		t.Fatalf("the account's own sign-in = %d", w.Code)
	}
	// asked again, not answered from the other sign-in's verdict
	if w := f.get(device, stranger, nil); w.Code != 403 || !strings.Contains(w.Body.String(), `"reason":"programme"`) {
		t.Fatalf("the child's device on a 16 programme = %d %s", w.Code, w.Body.String())
	}
	if w := f.get(plain, stranger, nil); w.Code != 200 {
		t.Fatalf("the other sign-in after the device was refused = %d", w.Code)
	}
	qs := f.v.questions()
	if len(qs) != 2 || qs[0].Family != "" || qs[0].Ceiling != nil || qs[1].Family != family || qs[1].Ceiling == nil || *qs[1].Ceiling != 9 {
		t.Fatalf("questions = %+v, want one per sign-in, the second with its family and ceiling", qs)
	}

	// the web port's gate, on loopback: the same sign-in in its headers
	gate := map[string]string{ViewerHeader: child.String(), ViewerSignInHeader: family, ViewerCeilingHeader: "9"}
	if w := f.get(chanPath, loopback, gate); w.Code != 403 {
		t.Fatalf("the gate's viewer on the child's device = %d", w.Code)
	}
	if w := f.get(chanPath, loopback, map[string]string{ViewerHeader: child.String()}); w.Code != 200 {
		t.Fatalf("the gate's viewer on another sign-in = %d", w.Code)
	}
	// a ceiling alone (a sign-in with no family)
	if w := f.get(chanPath, loopback, map[string]string{ViewerHeader: child.String(), ViewerCeilingHeader: "0"}); w.Code != 403 {
		t.Fatalf("the gate's viewer with a ceiling and no family = %d", w.Code)
	}
	// a sign-in that cannot be read names no one; from anywhere but this machine the
	// headers are nothing
	for _, bad := range []map[string]string{
		{ViewerHeader: child.String(), ViewerSignInHeader: "nope"},
		{ViewerHeader: child.String(), ViewerCeilingHeader: "nine"},
	} {
		if w := f.get(chanPath, loopback, bad); w.Code != 403 || f.served > 3 {
			t.Fatalf("an unreadable sign-in %v = %d", bad, w.Code)
		}
	}
	if w := f.get(chanPath, stranger, gate); w.Code != 401 {
		t.Fatalf("sign-in headers from a stranger = %d", w.Code)
	}
	// a token whose family is no id is no token
	if w := f.get(chanPath+"?sig="+f.token(t, func(c *auth.Claims) { c.Fam = "x" }), stranger, nil); w.Code == 200 {
		t.Fatal("a token with an unreadable family was served")
	}
}
