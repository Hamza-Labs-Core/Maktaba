// Package cftest is a fake Cloudflare API for the Epic 32 tests (32.6 to
// 32.10): token verify, live inputs, videos, storage use, signing keys,
// the GraphQL analytics probe, the customer-domain lifecycle endpoint and
// live outputs (simulcast, 32.13),
// with switches to make each one fail.
package cftest

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

// Server is the fake. Lock Mu to change its fields while it serves.
type Server struct {
	Mu sync.Mutex
	*httptest.Server

	Account      string
	Token        string
	TokenStatus  string // active (default) | disabled | expired
	AccountToken bool   // the token is account-owned: /user/tokens/verify answers 401
	CustomerCode string

	NoStream        bool // live inputs answer 403 (no Stream subscription)
	AnalyticsDenied bool // GraphQL answers an authorization error
	KeysDenied      bool // POST /stream/keys answers 403 (no Stream Write)
	Fail5xx         int  // the next N calls answer 503 (retry tests)

	StorageMinutes float64
	StorageLimit   float64

	Inputs map[string]*cf.LiveInput
	Videos map[string]*cf.Video
	Keys   map[string]cf.SigningKey
	Lives  map[string]cf.Lifecycle // input uid -> lifecycle
	// Views is GET /{input}/views's liveViewers per input; an input missing
	// here answers without the field (a hidden viewer count).
	Views   map[string]int
	KeyPost int // POST /stream/keys calls
	// Requests are "METHOD path" of every call.
	Requests []string

	// Playback (customer domain): a token (JWT, signature not checked)
	// whose sub is a live input's in-progress video plays; with
	// InputSubPlays one whose sub is the input id plays too.
	// SegmentExp makes segments check exp as well as playlists.
	InputSubPlays bool
	SegmentExp    bool
	// Now is the fake's clock for exp (default time.Now).
	Now func() time.Time

	// VerifySignatures: playback checks the token's RS256 signature against the
	// signing key named by its kid (a deleted key plays nothing, R8.4).
	VerifySignatures bool
	// IngestRTMPS / IngestSRT replace the ingest URLs of new inputs (a fake
	// ingest server in the end-to-end tests).
	IngestRTMPS string
	IngestSRT   string
	// MinutesByUID answers the per-video analytics query (minutes delivered
	// today per video id).
	MinutesByUID map[string]float64
	// Status is a live input's status answer (default "connected" while live).
	Status map[string]json.RawMessage

	// Outputs are the live inputs' outputs (simulcast, 32.13): input uid ->
	// output uid -> output (with its stream key). Deleting an input deletes
	// its outputs, as Cloudflare does.
	Outputs map[string]map[string]*cf.Output
	// OutputStatus: outputs answer a "status" field (README Q13 positive);
	// OutputStatusOf is its value per output uid (default "connected").
	OutputStatus   bool
	OutputStatusOf map[string]string
	// OutputFail: the next N output creates answer 400 with this message.
	OutputFail    int
	OutputFailMsg string

	// Forward, when set, plays Cloudflare's retransmission: while an
	// enabled output sits on a live input, the fake keeps one Forward(url,
	// key) running (a publisher to the destination) and stops it when the
	// output is deleted or disabled, or the broadcast ends. Called without Mu.
	Forward  func(url, key string) (stop func())
	forwards map[string]func()
}

// syncForwards starts and stops the retransmissions (see Forward).
func (s *Server) syncForwards() {
	s.Mu.Lock()
	if s.Forward == nil {
		s.Mu.Unlock()
		return
	}
	if s.forwards == nil {
		s.forwards = map[string]func(){}
	}
	type job struct{ url, key, id string }
	var start []job
	var stop []func()
	want := map[string]bool{}
	for in, outs := range s.Outputs {
		lc := s.Lives[in]
		for id, o := range outs {
			if lc.Live && o.Enabled != nil && *o.Enabled && s.Inputs[in] != nil {
				want[id] = true
				if _, ok := s.forwards[id]; !ok {
					s.forwards[id] = nil // reserved
					start = append(start, job{o.URL, o.StreamKey, id})
				}
			}
		}
	}
	for id, f := range s.forwards {
		if !want[id] {
			if f != nil {
				stop = append(stop, f)
			}
			delete(s.forwards, id)
		}
	}
	fwd := s.Forward
	s.Mu.Unlock()
	for _, f := range stop {
		f()
	}
	for _, j := range start {
		f := fwd(j.url, j.key)
		s.Mu.Lock()
		if _, ok := s.forwards[j.id]; ok {
			s.forwards[j.id] = f
			f = nil
		}
		s.Mu.Unlock()
		if f != nil {
			f() // deleted meanwhile
		}
	}
}

// StopForwards stops every retransmission (test cleanup).
func (s *Server) StopForwards() {
	s.Mu.Lock()
	var fs []func()
	for id, f := range s.forwards {
		if f != nil {
			fs = append(fs, f)
		}
		delete(s.forwards, id)
	}
	s.Forward = nil
	s.Mu.Unlock()
	for _, f := range fs {
		f()
	}
}

// InputOutputs is a copy of an input's outputs.
func (s *Server) InputOutputs(input string) []cf.Output {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	out := []cf.Output{}
	for _, o := range s.Outputs[input] {
		out = append(out, *o)
	}
	return out
}

// OutputCount is how many outputs all inputs hold.
func (s *Server) OutputCount() int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	n := 0
	for _, m := range s.Outputs {
		n += len(m)
	}
	return n
}

// outputJSON is an output as the fake answers it.
func (s *Server) outputJSON(o *cf.Output) map[string]any {
	m := map[string]any{"uid": o.UID, "url": o.URL, "streamKey": o.StreamKey, "enabled": o.Enabled != nil && *o.Enabled}
	if s.OutputStatus {
		st := "connected"
		if v, ok := s.OutputStatusOf[o.UID]; ok {
			st = v
		}
		m["status"] = st
	}
	return m
}

func (s *Server) serveOutputs(w http.ResponseWriter, r *http.Request, input, rest string) {
	if s.Outputs == nil {
		s.Outputs = map[string]map[string]*cf.Output{}
	}
	outs := s.Outputs[input]
	if outs == nil {
		outs = map[string]*cf.Output{}
		s.Outputs[input] = outs
	}
	if rest == "" {
		switch r.Method {
		case "POST":
			if s.OutputFail > 0 {
				s.OutputFail--
				msg := s.OutputFailMsg
				if msg == "" {
					msg = "output rejected"
				}
				write(w, http.StatusBadRequest, nil, cf.Error{Code: 10010, Message: msg})
				return
			}
			var req struct {
				URL       string `json:"url"`
				StreamKey string `json:"streamKey"`
				Enabled   *bool  `json:"enabled"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.URL == "" || req.StreamKey == "" {
				write(w, http.StatusBadRequest, nil, cf.Error{Code: 10005, Message: "url and streamKey are required"})
				return
			}
			on := true
			if req.Enabled != nil {
				on = *req.Enabled
			}
			if len(outs) >= 50 {
				write(w, http.StatusBadRequest, nil, cf.Error{Code: 10011, Message: "at most 50 outputs per live input"})
				return
			}
			o := &cf.Output{UID: uid(), URL: req.URL, StreamKey: req.StreamKey, Enabled: &on}
			outs[o.UID] = o
			write(w, 200, s.outputJSON(o))
		case "GET":
			list := []map[string]any{}
			for _, o := range outs {
				list = append(list, s.outputJSON(o))
			}
			write(w, 200, list)
		default:
			write(w, 405, nil, cf.Error{Code: 10000, Message: "method"})
		}
		return
	}
	o := outs[rest]
	if o == nil {
		write(w, 404, nil, cf.Error{Code: 10003, Message: "Not Found"})
		return
	}
	switch r.Method {
	case "DELETE":
		delete(outs, rest)
		write(w, 200, nil)
	case "PUT":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if e, ok := body["enabled"].(bool); ok {
			o.Enabled = &e
		}
		write(w, 200, s.outputJSON(o))
	default:
		write(w, 200, s.outputJSON(o))
	}
}

// claims decodes a token's payload (no signature check).
func claims(tok string) (cf.TokenClaims, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return cf.TokenClaims{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cf.TokenClaims{}, false
	}
	var c cf.TokenClaims
	return c, json.Unmarshal(b, &c) == nil
}

// playable reports whether sub names a live broadcast (caller holds Mu).
func (s *Server) playable(sub string) bool {
	for in, lc := range s.Lives {
		if lc.Live && (lc.VideoUID == sub || (s.InputSubPlays && in == sub)) {
			return true
		}
	}
	return false
}

func (s *Server) servePlayback(w http.ResponseWriter, tok, file string) {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	c, ok := claims(tok)
	if !ok {
		http.Error(w, "unsigned", http.StatusUnauthorized)
		return
	}
	if s.VerifySignatures && !s.signed(tok, c.Kid) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	expired := c.Exp <= now.Unix()
	segment := strings.HasSuffix(file, ".ts")
	if !s.playable(c.Sub) || (expired && (!segment || s.SegmentExp)) {
		http.Error(w, "denied", http.StatusUnauthorized)
		return
	}
	switch {
	case file == "video.m3u8":
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=6000000\nstream_0.m3u8\n"))
	case file == "stream_0.m3u8":
		pdt := now.Add(-8 * time.Second).UTC().Format(time.RFC3339Nano)
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-PROGRAM-DATE-TIME:" + pdt + "\n#EXTINF:2.0,\nseg_1.ts\n"))
	case segment:
		_, _ = w.Write([]byte("TS"))
	default:
		http.NotFound(w, nil)
	}
}

// signed checks tok's RS256 signature with the key kid (caller holds Mu).
func (s *Server) signed(tok, kid string) bool {
	k, ok := s.Keys[kid]
	if !ok {
		return false
	}
	priv, err := cf.ParseSigningKey(k.PEM)
	if err != nil {
		return false
	}
	return VerifyToken(tok, &priv.PublicKey) == nil
}

// VerifyToken checks a compact RS256 JWS against pub.
func VerifyToken(tok string, pub *rsa.PublicKey) error {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return errors.New("cftest: not a compact JWS")
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	var h struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(hdr, &h) != nil || h.Alg != "RS256" {
		return errors.New("cftest: alg is not RS256")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig)
}

// Claims decodes a token's claims (no signature check).
func Claims(tok string) (cf.TokenClaims, bool) { return claims(tok) }

// PublicKey is the public half of signing key kid.
func (s *Server) PublicKey(kid string) (*rsa.PublicKey, bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	k, ok := s.Keys[kid]
	if !ok {
		return nil, false
	}
	priv, err := cf.ParseSigningKey(k.PEM)
	if err != nil {
		return nil, false
	}
	return &priv.PublicKey, true
}

// GoLive starts a broadcast on an input as Cloudflare does when a publisher
// connects: a new live-inprogress video and lifecycle live. It returns the
// video id; an input already live keeps its video (a reconnect within
// timeoutSeconds continues the broadcast).
func (s *Server) GoLive(input string) string {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if lc, ok := s.Lives[input]; ok && lc.Live {
		return lc.VideoUID
	}
	v := &cf.Video{UID: uid(), LiveInput: input}
	v.Status.State = "live-inprogress"
	v.Playback.HLS = "https://customer-" + s.CustomerCode + ".cloudflarestream.com/" + v.UID + "/manifest/video.m3u8"
	s.Videos[v.UID] = v
	s.Lives[input] = cf.Lifecycle{IsInput: true, VideoUID: v.UID, Live: true}
	s.Mu.Unlock()
	s.syncForwards()
	s.Mu.Lock()
	return v.UID
}

// EndLive ends an input's broadcast: lifecycle not live, its video ready (the
// recording).
func (s *Server) EndLive(input string) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	lc, ok := s.Lives[input]
	if !ok || !lc.Live {
		return
	}
	if v := s.Videos[lc.VideoUID]; v != nil {
		v.Status.State = "ready"
		v.Duration = 60
	}
	s.Lives[input] = cf.Lifecycle{IsInput: true}
	s.Mu.Unlock()
	s.syncForwards()
	s.Mu.Lock()
}

// InputByStreamKey finds the input whose RTMPS stream key (or SRT
// passphrase) is key.
func (s *Server) InputByStreamKey(key string) (string, bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	for id, in := range s.Inputs {
		if (in.RTMPS != nil && in.RTMPS.StreamKey == key) || (in.SRT != nil && in.SRT.Passphrase == key) {
			return id, true
		}
	}
	return "", false
}

// Enabled reports whether an input is enabled.
func (s *Server) Enabled(input string) bool {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	in := s.Inputs[input]
	return in != nil && in.Enabled != nil && *in.Enabled
}

// New starts a fake for account "acc1" and token "tok-secret".
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{
		Account: "acc1", Token: "tok-secret", TokenStatus: "active", CustomerCode: "abc123xyz",
		StorageLimit: 1000, Inputs: map[string]*cf.LiveInput{}, Videos: map[string]*cf.Video{},
		Keys: map[string]cf.SigningKey{}, Lives: map[string]cf.Lifecycle{},
		Outputs: map[string]map[string]*cf.Output{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serve(w, r)
		s.syncForwards()
	}))
	t.Cleanup(s.Close)
	return s
}

// Client returns a cf.Client wired to the fake (instant retries).
func (s *Server) Client(account, token string) *cf.Client {
	c := cf.New(account, token)
	c.BaseURL = s.URL + "/client/v4"
	c.CustomerBase = s.URL + "/customer"
	c.HTTP = s.Server.Client()
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

// Count returns how many requests matched "METHOD path-prefix".
func (s *Server) Count(prefix string) int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	n := 0
	for _, r := range s.Requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func uid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func write(w http.ResponseWriter, status int, result any, errs ...cf.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if errs == nil {
		errs = []cf.Error{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": status < 300 && len(errs) == 0, "errors": errs, "messages": []any{}, "result": result,
	})
}

// keyPEM is an RSA key as Cloudflare returns it (base64 PEM). One per test
// binary: a 2048-bit key takes ~100 ms to make (seconds under -race), and
// the fake's keys differ by ID, which is all the tests compare.
var (
	keyOnce sync.Once
	keyPEM  string
)

func newKeyPEM() string {
	keyOnce.Do(func() {
		k, _ := rsa.GenerateKey(rand.Reader, 2048)
		p := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
		keyPEM = base64.StdEncoding.EncodeToString(p)
	})
	return keyPEM
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	path := r.URL.Path
	s.Requests = append(s.Requests, r.Method+" "+path)

	if strings.HasPrefix(path, "/customer/") {
		parts := strings.Split(strings.TrimPrefix(path, "/customer/"), "/")
		if len(parts) == 2 && parts[1] == "lifecycle" {
			lc, ok := s.Lives[parts[0]]
			if !ok {
				lc = cf.Lifecycle{IsInput: s.Inputs[parts[0]] != nil}
			}
			_ = json.NewEncoder(w).Encode(lc)
			return
		}
		if len(parts) == 2 && parts[1] == "views" {
			if n, ok := s.Views[parts[0]]; ok {
				_ = json.NewEncoder(w).Encode(map[string]int{"liveViewers": n})
			} else {
				_, _ = w.Write([]byte(`{}`))
			}
			return
		}
		if len(parts) == 3 && parts[1] == "manifest" {
			s.servePlayback(w, parts[0], parts[2])
			return
		}
		http.NotFound(w, r)
		return
	}
	if s.Fail5xx > 0 {
		s.Fail5xx--
		write(w, http.StatusServiceUnavailable, nil, cf.Error{Code: 10500, Message: "try again"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+s.Token {
		write(w, http.StatusUnauthorized, nil, cf.Error{Code: 1000, Message: "Invalid API Token"})
		return
	}
	p := strings.TrimPrefix(path, "/client/v4")
	acct := "/accounts/" + s.Account
	switch {
	case p == "/user/tokens/verify" && r.Method == "GET":
		if s.AccountToken {
			write(w, http.StatusUnauthorized, nil, cf.Error{Code: 1000, Message: "Invalid API Token"})
			return
		}
		write(w, 200, map[string]any{"id": "tokid", "status": s.TokenStatus})
	case p == acct+"/tokens/verify" && r.Method == "GET":
		write(w, 200, map[string]any{"id": "tokid", "status": s.TokenStatus})
	case strings.HasPrefix(p, "/accounts/") && !strings.HasPrefix(p, acct+"/"):
		write(w, http.StatusForbidden, nil, cf.Error{Code: 10000, Message: "Authentication error"})
	case p == acct+"/stream/live_inputs":
		if s.NoStream {
			write(w, http.StatusForbidden, nil, cf.Error{Code: 10023, Message: "Stream is not enabled for this account"})
			return
		}
		if r.Method == "POST" {
			var req cf.CreateLiveInput
			_ = json.NewDecoder(r.Body).Decode(&req)
			rtmpsURL, srtURL := "rtmps://live.cloudflare.com:443/live/", "srt://live.cloudflare.com:778"
			if s.IngestRTMPS != "" {
				rtmpsURL = s.IngestRTMPS
			}
			if s.IngestSRT != "" {
				srtURL = s.IngestSRT
			}
			in := &cf.LiveInput{UID: uid(), Meta: req.Meta, Enabled: req.Enabled, Recording: req.Recording,
				PreferLowLatency: req.PreferLowLatency,
				RTMPS:            &cf.Ingest{URL: rtmpsURL, StreamKey: "key-" + uid()},
				SRT:              &cf.Ingest{URL: srtURL, StreamID: "sid-" + uid()[:8], Passphrase: "pp-" + uid()[:12]}}
			s.Inputs[in.UID] = in
			write(w, 200, in)
			return
		}
		list := make([]cf.LiveInput, 0, len(s.Inputs))
		for _, in := range s.Inputs {
			x := *in
			x.RTMPS, x.SRT = nil, nil
			list = append(list, x)
		}
		write(w, 200, map[string]any{"liveInputs": list, "range": 1000, "total": len(list)})
	case strings.HasPrefix(p, acct+"/stream/live_inputs/"):
		rest := strings.TrimPrefix(p, acct+"/stream/live_inputs/")
		id, sub, _ := strings.Cut(rest, "/")
		in := s.Inputs[id]
		if in == nil {
			write(w, 404, nil, cf.Error{Code: 10003, Message: "Not Found"})
			return
		}
		switch {
		case sub == "outputs" || strings.HasPrefix(sub, "outputs/"):
			s.serveOutputs(w, r, id, strings.TrimPrefix(strings.TrimPrefix(sub, "outputs"), "/"))
		case sub == "videos":
			var vs []cf.Video
			for _, v := range s.Videos {
				if v.LiveInput == id {
					vs = append(vs, *v)
				}
			}
			write(w, 200, vs)
		case r.Method == "DELETE":
			delete(s.Inputs, id)
			delete(s.Outputs, id)
			write(w, 200, nil)
		case r.Method == "PUT":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if e, ok := body["enabled"].(bool); ok {
				in.Enabled = &e
			}
			write(w, 200, in)
		default:
			x := *in
			if st, ok := s.Status[id]; ok {
				x.Status = st
			} else if lc, ok := s.Lives[id]; ok && lc.Live {
				x.Status = json.RawMessage(`{"current":{"state":"connected"}}`)
			}
			write(w, 200, x)
		}
	case p == acct+"/stream/storage-usage":
		write(w, 200, cf.StorageUsage{TotalStorageMinutes: s.StorageMinutes, TotalStorageMinutesLimit: s.StorageLimit, VideoCount: len(s.Videos)})
	case p == acct+"/stream/keys":
		if r.Method == "POST" {
			s.KeyPost++
			if s.KeysDenied {
				write(w, http.StatusForbidden, nil, cf.Error{Code: 10000, Message: "Authentication error"})
				return
			}
			k := cf.SigningKey{ID: uid(), PEM: newKeyPEM(), JWK: "jwk", Created: "2026-10-19T00:00:00Z"}
			s.Keys[k.ID] = k
			write(w, 200, k)
			return
		}
		list := []cf.SigningKey{}
		for id, k := range s.Keys {
			list = append(list, cf.SigningKey{ID: id, Created: k.Created})
		}
		write(w, 200, list)
	case strings.HasPrefix(p, acct+"/stream/keys/") && r.Method == "DELETE":
		delete(s.Keys, strings.TrimPrefix(p, acct+"/stream/keys/"))
		write(w, 200, "ok")
	case p == acct+"/stream" && r.Method == "GET":
		vs := []cf.Video{}
		for _, v := range s.Videos {
			vs = append(vs, *v)
		}
		write(w, 200, vs)
	case strings.HasPrefix(p, acct+"/stream/") && r.Method == "DELETE":
		id := strings.TrimPrefix(p, acct+"/stream/")
		delete(s.Videos, id)
		write(w, 200, nil)
	case p == "/graphql" && r.Method == "POST":
		w.Header().Set("Content-Type", "application/json")
		if s.AnalyticsDenied {
			_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"not authorized for that account"}]}`))
			return
		}
		var q struct {
			Variables struct {
				UIDs []string `json:"uids"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		if q.Variables.UIDs != nil {
			day := time.Now().UTC().Format("2006-01-02")
			if s.Now != nil {
				day = s.Now().UTC().Format("2006-01-02")
			}
			groups := []map[string]any{}
			for _, u := range q.Variables.UIDs {
				if m, ok := s.MinutesByUID[u]; ok {
					groups = append(groups, map[string]any{"sum": map[string]any{"minutesViewed": m},
						"dimensions": map[string]any{"date": day, "uid": u}})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"viewer": map[string]any{
				"accounts": []any{map[string]any{"streamMinutesViewedAdaptiveGroups": groups}}}}, "errors": nil})
			return
		}
		_, _ = w.Write([]byte(`{"data":{"viewer":{"accounts":[{"streamMinutesViewedAdaptiveGroups":[{"sum":{"minutesViewed":12},"dimensions":{"date":"2026-10-18"}}]}]}},"errors":null}`))
	default:
		write(w, 404, nil, cf.Error{Code: 7003, Message: "Could not route to " + p})
	}
}

// AddVideo adds a video of an input with the account's playback domain.
func (s *Server) AddVideo(input, state string) *cf.Video {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	v := &cf.Video{UID: uid(), LiveInput: input}
	v.Status.State = state
	v.Playback.HLS = "https://customer-" + s.CustomerCode + ".cloudflarestream.com/" + v.UID + "/manifest/video.m3u8"
	s.Videos[v.UID] = v
	return v
}
