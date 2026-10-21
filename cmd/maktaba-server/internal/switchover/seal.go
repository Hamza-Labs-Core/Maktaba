package switchover

import (
	"bufio"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

// The kit travels sealed (specs/epics/37-node-replica, "Switching the home"): a key
// both ends derive from the home's cluster key and the machine's own key (X25519, then
// HKDF-SHA256 with the switchover's id), so only the machine taking over can open it and
// only the holder of the cluster key can have made it; in chunks of ChunkSize, each
// sealed with ChaCha20-Poly1305 under its index (nonce and associated data), the last one
// marked, so a chunk cannot be dropped, moved or added unseen.

// ChunkSize is how much of the kit one sealed chunk carries.
const ChunkSize = 1 << 20

// KitKey is the kit's key between priv (one end's X25519 private key) and peer (the
// other end's public key) for switchover id.
func KitKey(priv, peer []byte, id string) ([]byte, error) {
	shared, err := curve25519.X25519(priv, peer)
	if err != nil {
		return nil, fmt.Errorf("switchover: the kit's key: %w", err)
	}
	return hkdf.Key(sha256.New, shared, []byte(id), "maktaba-home-kit", chacha20poly1305.KeySize)
}

// SealNotice seals a switch's "moved" notice (the new home's address) for one machine,
// between the cluster key's private half and the machine's public key, so only that
// machine opens it and only the holder of the cluster key can have made it: base64 of a
// random nonce and the sealed notice, the switchover's id its associated data.
func SealNotice(clusterPriv, nodePub []byte, id string, notice []byte) (string, error) {
	aead, err := noticeAEAD(clusterPriv, nodePub, id)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, notice, []byte(id))), nil
}

// OpenNotice opens a machine's "moved" notice with its private key and the cluster's
// public key.
func OpenNotice(nodePriv, clusterPub []byte, id, sealed string) ([]byte, error) {
	aead, err := noticeAEAD(nodePriv, clusterPub, id)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < aead.NonceSize() {
		return nil, errors.New("switchover: a notice that is not one")
	}
	out, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(id))
	if err != nil {
		return nil, errors.New("switchover: a notice that does not open (not sealed for this machine by its cluster)")
	}
	return out, nil
}

func noticeAEAD(priv, peer []byte, id string) (cipher.AEAD, error) {
	return pairAEAD(priv, peer, id, "maktaba-home-moved")
}

// pairAEAD is the AEAD between one end's X25519 private key and the other's public key,
// for salt and purpose (HKDF-SHA256 info).
func pairAEAD(priv, peer []byte, salt, info string) (cipher.AEAD, error) {
	shared, err := curve25519.X25519(priv, peer)
	if err != nil {
		return nil, fmt.Errorf("switchover: the key of %s: %w", info, err)
	}
	key, err := hkdf.Key(sha256.New, shared, []byte(salt), info, chacha20poly1305.KeySize)
	if err != nil {
		return nil, err
	}
	return chacha20poly1305.New(key)
}

// ReportInfo is the purpose an old home's report of what it kept aside is sealed for
// (the new home's API opens it: api/internal/cluster/switchover).
const ReportInfo = "maktaba-kept-aside"

// SealReport seals an old home's report of what it wrote after an emergency's cut, from
// its machine key (oldPriv: the identity the standby kit registered it with) to the
// cluster's public key, for term: only the new home (which holds the cluster key) opens
// it, and only the old home can have made it. base64 of a random nonce and the sealed
// report, the term its associated data.
func SealReport(oldPriv, clusterPub []byte, term int64, report []byte) (string, error) {
	salt := fmt.Sprintf("term-%d", term)
	aead, err := pairAEAD(oldPriv, clusterPub, salt, ReportInfo)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, report, []byte(salt))), nil
}

// OpenReport opens SealReport's report with the cluster key's private half and the old
// home's public key.
func OpenReport(clusterPriv, oldPub []byte, term int64, sealed string) ([]byte, error) {
	salt := fmt.Sprintf("term-%d", term)
	aead, err := pairAEAD(clusterPriv, oldPub, salt, ReportInfo)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < aead.NonceSize() {
		return nil, errors.New("switchover: a report that is not one")
	}
	out, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(salt))
	if err != nil {
		return nil, errors.New("switchover: a report that does not open (not sealed by that machine for this cluster)")
	}
	return out, nil
}

func chunkAD(id string, i uint64, last bool) []byte {
	ad := make([]byte, 0, len(id)+9)
	ad = append(ad, id...)
	ad = binary.BigEndian.AppendUint64(ad, i)
	if last {
		return append(ad, 1)
	}
	return append(ad, 0)
}

func chunkNonce(i uint64) []byte {
	n := make([]byte, chacha20poly1305.NonceSize)
	binary.BigEndian.PutUint64(n[4:], i)
	return n
}

// Sealer seals a stream into chunks: Write the kit, Close seals the last one; each sealed
// chunk goes to emit (with its index and whether it is the last).
type Sealer struct {
	key  []byte
	id   string
	emit func(i uint64, last bool, sealed []byte) error
	buf  []byte
	n    uint64
	err  error
}

// NewSealer seals for switchover id under key.
func NewSealer(key []byte, id string, emit func(i uint64, last bool, sealed []byte) error) *Sealer {
	return &Sealer{key: key, id: id, emit: emit}
}

func (s *Sealer) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	n := len(p)
	for len(p) > 0 {
		take := min(ChunkSize-len(s.buf), len(p))
		s.buf = append(s.buf, p[:take]...)
		p = p[take:]
		if len(s.buf) == ChunkSize {
			// a full chunk is sealed once the next byte shows it is not the last
			if len(p) == 0 {
				break
			}
			if s.err = s.flush(false); s.err != nil {
				return 0, s.err
			}
		}
	}
	return n, nil
}

func (s *Sealer) flush(last bool) error {
	a, err := chacha20poly1305.New(s.key)
	if err != nil {
		return err
	}
	sealed := a.Seal(nil, chunkNonce(s.n), s.buf, chunkAD(s.id, s.n, last))
	if err := s.emit(s.n, last, sealed); err != nil {
		return err
	}
	s.n++
	s.buf = s.buf[:0]
	return nil
}

// Close seals what is left as the last chunk.
func (s *Sealer) Close() error {
	if s.err != nil {
		return s.err
	}
	return s.flush(true)
}

// ErrIncomplete: the chunks ended before the last one.
var ErrIncomplete = errors.New("switchover: the kit is incomplete")

// Opener opens chunks in order, giving the stream they carry.
type Opener struct {
	key  []byte
	id   string
	n    uint64
	done bool
	w    io.Writer
}

// NewOpener opens chunks of switchover id under key into w.
func NewOpener(key []byte, id string, w io.Writer) *Opener {
	return &Opener{key: key, id: id, w: w}
}

// Chunk opens the next chunk (i must be the next index).
func (o *Opener) Chunk(i uint64, last bool, sealed []byte) error {
	if o.done || i != o.n {
		return fmt.Errorf("switchover: kit chunk %d out of order (want %d)", i, o.n)
	}
	a, err := chacha20poly1305.New(o.key)
	if err != nil {
		return err
	}
	plain, err := a.Open(nil, chunkNonce(i), sealed, chunkAD(o.id, i, last))
	if err != nil {
		return fmt.Errorf("switchover: kit chunk %d does not open: %w", i, err)
	}
	if _, err := o.w.Write(plain); err != nil {
		return err
	}
	o.n++
	o.done = last
	return nil
}

// Done reports whether the last chunk was opened.
func (o *Opener) Done() bool { return o.done }

// Sealed chunks are kept in a file as records: index (8 bytes), last (1), length (4),
// the sealed bytes.

// WriteChunk appends one sealed chunk record to w.
func WriteChunk(w io.Writer, i uint64, last bool, sealed []byte) error {
	var h [13]byte
	binary.BigEndian.PutUint64(h[:8], i)
	if last {
		h[8] = 1
	}
	binary.BigEndian.PutUint32(h[9:], uint32(len(sealed)))
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err := w.Write(sealed)
	return err
}

// ReadChunks calls fn for each sealed chunk record in r.
func ReadChunks(r io.Reader, fn func(i uint64, last bool, sealed []byte) error) error {
	br := bufio.NewReader(r)
	for {
		var h [13]byte
		if _, err := io.ReadFull(br, h[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		size := binary.BigEndian.Uint32(h[9:])
		if size > ChunkSize+chacha20poly1305.Overhead {
			return fmt.Errorf("switchover: a kit chunk of %d bytes", size)
		}
		sealed := make([]byte, size)
		if _, err := io.ReadFull(br, sealed); err != nil {
			return err
		}
		if err := fn(binary.BigEndian.Uint64(h[:8]), h[8] == 1, sealed); err != nil {
			return err
		}
	}
}
