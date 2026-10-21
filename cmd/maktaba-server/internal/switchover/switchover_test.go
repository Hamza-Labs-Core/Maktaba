package switchover

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The kit is sealed between the home's cluster key and the machine's own: the two ends
// derive the same key, anyone else cannot; a chunk dropped, moved or added, or the last
// one missing, is refused.
func TestTheKitIsSealedForOneMachine(t *testing.T) {
	homePriv, homePub := keypair(t)
	nodePriv, nodePub := keypair(t)
	otherPriv, _ := keypair(t)
	send, _ := KitKey(homePriv, nodePub, "sw-1")
	recv, _ := KitKey(nodePriv, homePub, "sw-1")
	if !bytes.Equal(send, recv) {
		t.Fatal("the two ends derive different keys")
	}
	if other, _ := KitKey(otherPriv, homePub, "sw-1"); bytes.Equal(other, send) {
		t.Fatal("another machine derives the key")
	}
	if again, _ := KitKey(homePriv, nodePub, "sw-2"); bytes.Equal(again, send) {
		t.Fatal("another switchover's key is the same")
	}
	payload := bytes.Repeat([]byte("kit "), ChunkSize/2+25) // two chunks and a bit
	type chunk struct {
		i      uint64
		last   bool
		sealed []byte
	}
	var chunks []chunk
	s := NewSealer(send, "sw-1", func(i uint64, last bool, sealed []byte) error {
		chunks = append(chunks, chunk{i, last, append([]byte(nil), sealed...)})
		return nil
	})
	if _, err := s.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 || !chunks[2].last || chunks[1].last {
		t.Fatalf("%d chunks", len(chunks))
	}
	open := func(cs []chunk) ([]byte, bool, error) {
		var out bytes.Buffer
		o := NewOpener(recv, "sw-1", &out)
		for _, c := range cs {
			if err := o.Chunk(c.i, c.last, c.sealed); err != nil {
				return nil, false, err
			}
		}
		return out.Bytes(), o.Done(), nil
	}
	if got, done, err := open(chunks); err != nil || !done || !bytes.Equal(got, payload) {
		t.Fatalf("opened: %v done %v equal %v", err, done, bytes.Equal(got, payload))
	}
	if _, done, err := open(chunks[:2]); err != nil || done {
		t.Fatal("a kit without its last chunk is taken for whole")
	}
	if _, _, err := open([]chunk{chunks[1], chunks[0]}); err == nil {
		t.Fatal("chunks out of order are taken")
	}
	moved := []chunk{chunks[0], {1, true, chunks[1].sealed}}
	if _, _, err := open(moved); err == nil {
		t.Fatal("a chunk marked last that was not is taken")
	}
	tampered := append([]chunk(nil), chunks...)
	tampered[0].sealed = append([]byte(nil), tampered[0].sealed...)
	tampered[0].sealed[3] ^= 1
	if _, _, err := open(tampered); err == nil {
		t.Fatal("a changed chunk is taken")
	}
	// the records a file keeps them in
	var file bytes.Buffer
	for _, c := range chunks {
		if err := WriteChunk(&file, c.i, c.last, c.sealed); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	o := NewOpener(recv, "sw-1", &out)
	if err := ReadChunks(&file, o.Chunk); err != nil || !o.Done() || !bytes.Equal(out.Bytes(), payload) {
		t.Fatalf("from the file: %v", err)
	}
}

func TestTheKitRoundTrips(t *testing.T) {
	k := &Kit{Switchover: "sw-1", Term: 3, Tables: map[string][]json.RawMessage{
		"home_secrets":  {json.RawMessage(`{"name":"ext.plex.token","ciphertext":"\\x01"}`)},
		"cluster_nodes": {},
	}, Sequences: []Sequence{{Name: "public.events_id_seq", LastValue: 42, IsCalled: true}},
		Roles: []Role{{Name: "maktaba_node_x", Verifier: "SCRAM-SHA-256$4096:a$b:c", Login: true, Groups: []string{"maktaba_worker"}}},
		Files: map[string][]byte{"secrets.key": {1, 2, 3}}}
	var buf bytes.Buffer
	if err := k.Write(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := ReadKit(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Term != 3 || len(got.Tables["home_secrets"]) != 1 || got.Tables["cluster_nodes"] == nil ||
		got.Sequences[0].LastValue != 42 || got.Roles[0].Groups[0] != "maktaba_worker" || !bytes.Equal(got.Files["secrets.key"], []byte{1, 2, 3}) {
		t.Fatalf("read back: %+v", got)
	}
	// cut short: not a kit
	if _, err := ReadKit(bytes.NewReader(buf.Bytes()[:buf.Len()/2])); err == nil {
		t.Fatal("half a kit is taken")
	}
}

// pgoutput messages as Postgres 16 sends them (protocol 1, text values).
func TestTheKeptAsideChangesAreDecoded(t *testing.T) {
	w := func(parts ...any) []byte {
		var b bytes.Buffer
		for _, p := range parts {
			switch v := p.(type) {
			case byte:
				b.WriteByte(v)
			case uint16:
				_ = binary.Write(&b, binary.BigEndian, v)
			case uint32:
				_ = binary.Write(&b, binary.BigEndian, v)
			case uint64:
				_ = binary.Write(&b, binary.BigEndian, v)
			case string:
				b.WriteString(v)
				b.WriteByte(0)
			case []byte:
				b.Write(v)
			}
		}
		return b.Bytes()
	}
	text := func(s string) []byte {
		return append(binary.BigEndian.AppendUint32([]byte{'t'}, uint32(len(s))), s...)
	}
	d := decoder{rels: map[uint32]relation{}}
	msgs := [][]byte{
		w(byte('R'), uint32(7), "public", "app_settings", byte('d'), uint16(2),
			byte(1), "key", uint32(25), uint32(0xffffffff), byte(0), "value", uint32(3802), uint32(0xffffffff)),
		w(byte('B'), uint64(0x100), uint64(1), uint32(9)),
		w(byte('I'), uint32(7), byte('N'), uint16(2), text("test.lost"), []byte{'n'}),
		w(byte('C'), byte(0), uint64(0x100), uint64(0x110), uint64(1)),
	}
	var got []Change
	var end uint64
	for _, m := range msgs {
		msg, err := d.message(m)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, msg.changes...)
		if msg.kind == 'C' {
			end = msg.end
		}
	}
	if len(got) != 1 || got[0].Table != "public.app_settings" || got[0].Kind != "insert" ||
		*got[0].Row["key"] != "test.lost" || got[0].Row["value"] != nil || end != 0x110 {
		t.Fatalf("decoded: %+v end %x", got, end)
	}
	if _, err := d.message(msgs[2][:5]); !errors.Is(err, errShort) {
		t.Fatalf("a short message: %v", err)
	}
	if n, err := parseLSN("0/110"); err != nil || n != 0x110 || formatLSN(n) != "0/110" {
		t.Fatalf("LSN: %x %v", n, err)
	}
	if !strings.HasPrefix(pgTime(0).String(), "2000-01-01") {
		t.Fatal("Postgres's epoch")
	}
}

// A switch's notice of the new home opens only on the machine it was sealed for, only
// as the cluster key made it, and only for its switch.
func TestANoticeOpensOnItsMachineOnly(t *testing.T) {
	clusterPriv, clusterPub := keypair(t)
	nodePriv, nodePub := keypair(t)
	otherPriv, _ := keypair(t)
	_, strangerPub := keypair(t)
	notice := []byte(`{"term":2,"home":{"api_url":"http://b:8080"}}`)
	sealed, err := SealNotice(clusterPriv, nodePub, "sw-1", notice)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "b:8080") {
		t.Fatal("the notice is in the clear")
	}
	got, err := OpenNotice(nodePriv, clusterPub, "sw-1", sealed)
	if err != nil || !bytes.Equal(got, notice) {
		t.Fatalf("its machine: %q %v", got, err)
	}
	if _, err := OpenNotice(otherPriv, clusterPub, "sw-1", sealed); err == nil {
		t.Fatal("another machine opened it")
	}
	if _, err := OpenNotice(nodePriv, strangerPub, "sw-1", sealed); err == nil {
		t.Fatal("it opened as another cluster's")
	}
	if _, err := OpenNotice(nodePriv, clusterPub, "sw-2", sealed); err == nil {
		t.Fatal("it opened for another switch")
	}
	raw := []byte(sealed)
	raw[len(raw)/2] ^= 1
	if _, err := OpenNotice(nodePriv, clusterPub, "sw-1", string(raw)); err == nil {
		t.Fatal("a changed notice opened")
	}
	if a, _ := SealNotice(clusterPriv, nodePub, "sw-1", notice); a == sealed {
		t.Fatal("two seals alike: the nonce is not random")
	}
}
