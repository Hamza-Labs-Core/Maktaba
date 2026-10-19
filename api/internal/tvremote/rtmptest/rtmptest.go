// Package rtmptest is a fake RTMP(S) ingest for the Epic 32 end-to-end tests:
// enough of RTMP (handshake, chunk stream, AMF0 connect/createStream/publish)
// for ffmpeg's rtmp/rtmps muxer to publish to it. It counts publisher
// connections per stream key and records every audio and video message's
// timestamp, so a test can assert one continuous session and monotonic
// timestamps across a source switch (Epic 33 story 33.8 TC1/TC2).
package rtmptest

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"
)

// Tag is one media message: type 8 audio or 9 video, its RTMP timestamp
// (milliseconds, the FLV DTS) and, for video, whether it is a keyframe or a
// sequence header.
type Tag struct {
	Type      byte
	TS        uint32
	Key       bool
	SeqHeader bool
	At        time.Time
}

// Publish is one publisher connection.
type Publish struct {
	Key    string
	Start  time.Time
	End    time.Time // zero while connected
	Tags   []Tag
	Remote string
}

// Server is the fake ingest.
type Server struct {
	ln  net.Listener
	TLS bool
	// OnPublish / OnClose are called when a publisher starts / disconnects.
	OnPublish func(key string)
	OnClose   func(key string)

	mu   sync.Mutex
	pubs []*Publish
	wg   sync.WaitGroup
	done chan struct{}
}

// Listen starts a server on 127.0.0.1 (TLS: RTMPS with a self-signed cert).
func Listen(useTLS bool) (*Server, error) {
	var ln net.Listener
	var err error
	if useTLS {
		cert, cerr := selfSigned()
		if cerr != nil {
			return nil, cerr
		}
		ln, err = tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, TLS: useTLS, done: make(chan struct{})}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// URL is the ingest base URL (rtmp(s)://127.0.0.1:port/live/); the stream key follows.
func (s *Server) URL() string {
	scheme := "rtmp"
	if s.TLS {
		scheme = "rtmps"
	}
	return fmt.Sprintf("%s://%s/live/", scheme, s.ln.Addr().String())
}

// Close stops the server.
func (s *Server) Close() {
	close(s.done)
	_ = s.ln.Close()
	s.wg.Wait()
}

// Publishes is a copy of every publisher connection so far.
func (s *Server) Publishes() []Publish {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Publish, 0, len(s.pubs))
	for _, p := range s.pubs {
		c := *p
		c.Tags = append([]Tag(nil), p.Tags...)
		out = append(out, c)
	}
	return out
}

// Active is how many publishers of key are connected now.
func (s *Server) Active(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.pubs {
		if p.Key == key && p.End.IsZero() {
			n++
		}
	}
	return n
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(c)
		}()
	}
}

func selfSigned() (tls.Certificate, error) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}, nil
}

// chunk stream state per chunk stream id
type csState struct {
	ts, delta uint32
	length    uint32
	typ       byte
	sid       uint32
	ext       bool
	buf       []byte
}

type conn struct {
	s       *Server
	c       net.Conn
	r       *bufio.Reader
	inChunk uint32
	cs      map[uint32]*csState
	pub     *Publish
}

func (s *Server) serve(nc net.Conn) {
	defer nc.Close()
	c := &conn{s: s, c: nc, r: bufio.NewReaderSize(nc, 64<<10), inChunk: 128, cs: map[uint32]*csState{}}
	if err := c.handshake(); err != nil {
		return
	}
	err := c.loop()
	_ = err
	if c.pub != nil {
		s.mu.Lock()
		c.pub.End = time.Now()
		key := c.pub.Key
		s.mu.Unlock()
		if s.OnClose != nil {
			s.OnClose(key)
		}
	}
}

func (c *conn) handshake() error {
	_ = c.c.SetDeadline(time.Now().Add(10 * time.Second))
	defer func() { _ = c.c.SetDeadline(time.Time{}) }()
	c0c1 := make([]byte, 1537)
	if _, err := io.ReadFull(c.r, c0c1); err != nil {
		return err
	}
	s1 := make([]byte, 1536)
	_, _ = rand.Read(s1[8:])
	out := append([]byte{3}, s1...)
	out = append(out, c0c1[1:]...) // S2 echoes C1
	if _, err := c.c.Write(out); err != nil {
		return err
	}
	c2 := make([]byte, 1536)
	_, err := io.ReadFull(c.r, c2)
	return err
}

func u24(b []byte) uint32 { return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]) }

func (c *conn) loop() error {
	for {
		select {
		case <-c.s.done:
			return nil
		default:
		}
		_ = c.c.SetReadDeadline(time.Now().Add(60 * time.Second))
		b0, err := c.r.ReadByte()
		if err != nil {
			return err
		}
		fmtT := b0 >> 6
		csid := uint32(b0 & 0x3f)
		switch csid {
		case 0:
			x, err := c.r.ReadByte()
			if err != nil {
				return err
			}
			csid = uint32(x) + 64
		case 1:
			x := make([]byte, 2)
			if _, err := io.ReadFull(c.r, x); err != nil {
				return err
			}
			csid = uint32(x[1])*256 + uint32(x[0]) + 64
		}
		st := c.cs[csid]
		if st == nil {
			st = &csState{}
			c.cs[csid] = st
		}
		var hdr []byte
		switch fmtT {
		case 0:
			hdr = make([]byte, 11)
		case 1:
			hdr = make([]byte, 7)
		case 2:
			hdr = make([]byte, 3)
		}
		if _, err := io.ReadFull(c.r, hdr); err != nil {
			return err
		}
		newMsg := len(st.buf) == 0
		var tsField uint32
		switch fmtT {
		case 0:
			tsField = u24(hdr)
			st.length, st.typ = u24(hdr[3:]), hdr[6]
			st.sid = binary.LittleEndian.Uint32(hdr[7:])
		case 1:
			tsField = u24(hdr)
			st.length, st.typ = u24(hdr[3:]), hdr[6]
		case 2:
			tsField = u24(hdr)
		}
		if fmtT < 3 {
			st.ext = tsField == 0xffffff
		}
		if st.ext {
			x := make([]byte, 4)
			if _, err := io.ReadFull(c.r, x); err != nil {
				return err
			}
			if fmtT < 3 {
				tsField = binary.BigEndian.Uint32(x)
			}
		}
		if newMsg {
			switch fmtT {
			case 0:
				st.ts, st.delta = tsField, 0
			case 1, 2:
				st.delta = tsField
				st.ts += tsField
			case 3:
				st.ts += st.delta
			}
		}
		n := st.length - uint32(len(st.buf))
		if n > c.inChunk {
			n = c.inChunk
		}
		part := make([]byte, n)
		if _, err := io.ReadFull(c.r, part); err != nil {
			return err
		}
		st.buf = append(st.buf, part...)
		if uint32(len(st.buf)) >= st.length {
			msg := st.buf
			st.buf = nil
			if err := c.message(st.typ, st.ts, st.sid, msg); err != nil {
				return err
			}
		}
	}
}

func (c *conn) message(typ byte, ts, _ uint32, b []byte) error {
	switch typ {
	case 1: // set chunk size
		if len(b) >= 4 {
			c.inChunk = binary.BigEndian.Uint32(b) & 0x7fffffff
		}
	case 20: // AMF0 command
		return c.command(b)
	case 8, 9:
		if c.pub == nil {
			return nil
		}
		t := Tag{Type: typ, TS: ts, At: time.Now()}
		if typ == 9 && len(b) >= 2 {
			t.Key = b[0]>>4 == 1
			t.SeqHeader = b[0]&0x0f == 7 && b[1] == 0
		}
		c.s.mu.Lock()
		c.pub.Tags = append(c.pub.Tags, t)
		c.s.mu.Unlock()
	}
	return nil
}

func (c *conn) command(b []byte) error {
	vals, _ := amfDecodeAll(b)
	if len(vals) < 2 {
		return nil
	}
	name, _ := vals[0].(string)
	tx, _ := vals[1].(float64)
	switch name {
	case "connect":
		_ = c.send(2, 5, 0, be32(2500000))
		_ = c.send(2, 6, 0, append(be32(2500000), 2))
		return c.send(3, 20, 0, amf(amfString("_result"), amfNumber(tx),
			amfObject([][2]any{{"fmsVer", amfString("FMS/3,0,1,123")}, {"capabilities", amfNumber(31)}}),
			amfObject([][2]any{{"level", amfString("status")}, {"code", amfString("NetConnection.Connect.Success")},
				{"description", amfString("Connection succeeded.")}, {"objectEncoding", amfNumber(0)}})))
	case "releaseStream", "FCPublish":
		return c.send(3, 20, 0, amf(amfString("_result"), amfNumber(tx), amfNull(), amfNull()))
	case "createStream":
		return c.send(3, 20, 0, amf(amfString("_result"), amfNumber(tx), amfNull(), amfNumber(1)))
	case "publish":
		key := ""
		if len(vals) >= 4 {
			key, _ = vals[3].(string)
		}
		key = strings.TrimPrefix(key, "/")
		p := &Publish{Key: key, Start: time.Now(), Remote: c.c.RemoteAddr().String()}
		c.s.mu.Lock()
		c.s.pubs = append(c.s.pubs, p)
		c.s.mu.Unlock()
		c.pub = p
		if c.s.OnPublish != nil {
			c.s.OnPublish(key)
		}
		return c.send(5, 20, 1, amf(amfString("onStatus"), amfNumber(0), amfNull(),
			amfObject([][2]any{{"level", amfString("status")}, {"code", amfString("NetStream.Publish.Start")},
				{"description", amfString("Publishing.")}})))
	}
	return nil
}

func be32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// send writes one message on csid in 128-byte chunks.
func (c *conn) send(csid uint32, typ byte, sid uint32, body []byte) error {
	var out []byte
	h := []byte{byte(csid & 0x3f), 0, 0, 0, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body)), typ, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(h[8:], sid)
	out = append(out, h...)
	for i := 0; i < len(body); i += 128 {
		if i > 0 {
			out = append(out, 0xc0|byte(csid&0x3f))
		}
		end := i + 128
		if end > len(body) {
			end = len(body)
		}
		out = append(out, body[i:end]...)
	}
	_, err := c.c.Write(out)
	return err
}

// AMF0

func amf(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func amfNumber(f float64) []byte {
	b := make([]byte, 9)
	b[0] = 0
	binary.BigEndian.PutUint64(b[1:], math.Float64bits(f))
	return b
}

func amfString(s string) []byte {
	b := []byte{2, byte(len(s) >> 8), byte(len(s))}
	return append(b, s...)
}

func amfNull() []byte { return []byte{5} }

func amfObject(kv [][2]any) []byte {
	b := []byte{3}
	for _, p := range kv {
		k := p[0].(string)
		b = append(b, byte(len(k)>>8), byte(len(k)))
		b = append(b, k...)
		b = append(b, p[1].([]byte)...)
	}
	return append(b, 0, 0, 9)
}

var errAMF = errors.New("rtmptest: bad AMF0")

func amfDecodeAll(b []byte) ([]any, error) {
	var out []any
	for len(b) > 0 {
		v, n, err := amfDecode(b)
		if err != nil {
			return out, err
		}
		out = append(out, v)
		b = b[n:]
	}
	return out, nil
}

func amfDecode(b []byte) (any, int, error) {
	if len(b) == 0 {
		return nil, 0, errAMF
	}
	switch b[0] {
	case 0:
		if len(b) < 9 {
			return nil, 0, errAMF
		}
		return math.Float64frombits(binary.BigEndian.Uint64(b[1:9])), 9, nil
	case 1:
		if len(b) < 2 {
			return nil, 0, errAMF
		}
		return b[1] != 0, 2, nil
	case 2:
		if len(b) < 3 {
			return nil, 0, errAMF
		}
		n := int(b[1])<<8 | int(b[2])
		if len(b) < 3+n {
			return nil, 0, errAMF
		}
		return string(b[3 : 3+n]), 3 + n, nil
	case 5, 6:
		return nil, 1, nil
	case 3, 8:
		i := 1
		if b[0] == 8 {
			i = 5
		}
		m := map[string]any{}
		for {
			if i+3 > len(b) {
				return nil, 0, errAMF
			}
			kl := int(b[i])<<8 | int(b[i+1])
			if kl == 0 && b[i+2] == 9 {
				return m, i + 3, nil
			}
			if i+2+kl > len(b) {
				return nil, 0, errAMF
			}
			k := string(b[i+2 : i+2+kl])
			i += 2 + kl
			v, n, err := amfDecode(b[i:])
			if err != nil {
				return nil, 0, err
			}
			m[k] = v
			i += n
		}
	}
	return nil, 0, errAMF
}
