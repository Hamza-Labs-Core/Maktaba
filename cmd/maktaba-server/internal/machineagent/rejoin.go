package machineagent

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

// A machine its home no longer takes (its sessions refused: the home removed or rejected
// it) asks the home to take it again as itself — its own node id and key, never a new
// identity — through the join an admin approves on the home's Machines page with the
// pairing code (design §3.2, the pipeline's join): the credentials the home seals to it
// once approved are what node.json keeps.

const (
	joinSASInfo  = "maktaba-join-sas"
	joinSealInfo = "maktaba-join-seal"
)

// Rejoin is this machine's request to its home to take it again.
type Rejoin struct {
	// HomeURL is the home's API (http(s)://host:port)
	HomeURL    string
	NodeID     string
	Name       string
	PrivateKey string // X25519, standard base64 (node.json's)
	// CertDir is where its cluster-link certificate lives (beside node.json): its key is
	// made there for the request, the certificate kept there once approved
	CertDir string
	Version string
	HTTP    *http.Client
}

// RejoinRefused is a request the home does not take: its status and error code
// (already_member: it still holds the machine, revoked, until an admin removes it;
// joins_closed; pending_elsewhere; rate_limited).
type RejoinRefused struct {
	Status int
	Code   string
	Detail string
}

func (e *RejoinRefused) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("the home refused the request (%s): %s", e.Code, e.Detail)
	}
	return fmt.Sprintf("the home refused the request (%d %s)", e.Status, e.Code)
}

// Joined is what the home sealed to the machine once an admin approved it.
type Joined struct {
	DatabaseURL string `json:"database_url"`
	DatabaseCA  string `json:"database_ca,omitempty"`
	DatabaseTLS string `json:"database_tls,omitempty"`
	NodeID      string `json:"node_id"`
	Home        struct {
		APIURL     string `json:"api_url"`
		ClusterPub string `json:"cluster_pub,omitempty"`
		InstanceID string `json:"instance_id"`
		Name       string `json:"name"`
	} `json:"home"`
	TLS *struct {
		Cert     string `json:"cert"`
		CACert   string `json:"ca_cert"`
		CASHA256 string `json:"ca_sha256"`
	} `json:"tls,omitempty"`
}

// PendingJoin is a request the home took: the pairing code the admin types, and its answer
// to wait for.
type PendingJoin struct {
	// Code is the six-digit pairing code (shown "761 353")
	Code     string
	HomeName string
	r        Rejoin
	sealKey  []byte
	pollURL  string
	csr      bool
}

func (r Rejoin) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Ask sends the join request with this machine's own id and key.
func (r Rejoin) Ask(ctx context.Context) (*PendingJoin, error) {
	priv, err := base64.StdEncoding.DecodeString(r.PrivateKey)
	if err != nil || len(priv) != 32 {
		return nil, errors.New("this machine's key in node.json does not read")
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(r.HomeURL, "/")
	body := map[string]any{"node_id": r.NodeID, "name": r.Name, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"versions": map[string]any{"server": r.Version}, "capabilities": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH},
		"storage": map[string]any{}, "pubkey": base64.StdEncoding.EncodeToString(pub)}
	csr := false
	if r.CertDir != "" {
		if c, err := newCSR(r.CertDir, r.NodeID); err == nil {
			body["csr"], csr = c, true // else it asks for its certificate on its session
		}
	}
	status, resp, err := r.call(ctx, http.MethodPost, base+"/api/cluster/join", body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK && status != http.StatusCreated && status != http.StatusAccepted {
		return nil, refusal(status, resp)
	}
	var ans struct {
		HomePubkey string `json:"home_pubkey"`
		Home       struct {
			Name string `json:"name"`
		} `json:"home"`
	}
	homePub, err := func() ([]byte, error) {
		if err := json.Unmarshal(resp, &ans); err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(ans.HomePubkey)
	}()
	if err != nil || len(homePub) != 32 {
		return nil, errors.New("the home's answer has no usable key")
	}
	shared, err := curve25519.X25519(priv, homePub)
	if err != nil {
		return nil, err
	}
	code, err := joinSAS(shared, pub, homePub)
	if err != nil {
		return nil, err
	}
	key, err := joinKey(shared, pub, homePub, joinSealInfo, 32)
	if err != nil {
		return nil, err
	}
	return &PendingJoin{Code: code, HomeName: ans.Home.Name, r: r, sealKey: key, csr: csr,
		pollURL: base + "/api/cluster/join/" + url.PathEscape(r.NodeID) + "?pubkey=" +
			url.QueryEscape(base64.StdEncoding.EncodeToString(pub))}, nil
}

// Wait polls the home every until it approves the request (its sealed credentials, the
// cluster-link certificate kept), or says no (rejected, expired), or ctx ends.
func (p *PendingJoin) Wait(ctx context.Context, every time.Duration) (Joined, error) {
	for {
		status, resp, err := p.r.call(ctx, http.MethodGet, p.pollURL, nil)
		if err == nil {
			var ans struct {
				State  string `json:"state"`
				Sealed *struct {
					Nonce      string `json:"nonce"`
					Ciphertext string `json:"ciphertext"`
				} `json:"sealed"`
			}
			_ = json.Unmarshal(resp, &ans)
			switch {
			case status == http.StatusOK && ans.State == "approved" && ans.Sealed != nil:
				return p.open(ans.Sealed.Nonce, ans.Sealed.Ciphertext)
			case ans.State == "rejected" || ans.State == "expired" || ans.State == "revoked":
				return Joined{}, &RejoinRefused{Status: status, Code: ans.State, Detail: "the home " + ans.State + " the request"}
			case (status == http.StatusForbidden || status == http.StatusNotFound || status == http.StatusGone) && ans.State == "":
				return Joined{}, &RejoinRefused{Status: status, Code: "unknown_request", Detail: "the home no longer knows the request"}
			}
		}
		select {
		case <-ctx.Done():
			return Joined{}, ctx.Err()
		case <-time.After(every):
		}
	}
}

func (p *PendingJoin) open(nonceB64, ctB64 string) (Joined, error) {
	nonce, err1 := base64.StdEncoding.DecodeString(nonceB64)
	ct, err2 := base64.StdEncoding.DecodeString(ctB64)
	if err1 != nil || err2 != nil {
		return Joined{}, errors.New("the home's credentials do not read")
	}
	plain, err := openSealed(p.sealKey, p.r.NodeID, nonce, ct)
	if err != nil {
		return Joined{}, errors.New("the home's credentials could not be opened with this machine's key")
	}
	var j Joined
	if err := json.Unmarshal(plain, &j); err != nil || !strings.EqualFold(j.NodeID, p.r.NodeID) || j.DatabaseURL == "" {
		return Joined{}, errors.New("the home's credentials are not for this machine")
	}
	if p.csr && j.TLS != nil && j.TLS.Cert != "" {
		// else it asks for its certificate on its session
		_ = saveCert(p.r.CertDir, j.TLS.Cert, j.TLS.CACert, j.TLS.CASHA256)
	}
	return j, nil
}

func (r Rejoin) call(ctx context.Context, method, u string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, err
}

// refusal is a join request's error answer (problem+json with a top-level code).
func refusal(status int, body []byte) error {
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Error  any    `json:"error"`
	}
	_ = json.Unmarshal(body, &p)
	if p.Code == "" {
		switch e := p.Error.(type) {
		case string:
			p.Code = e
		case map[string]any:
			p.Code, _ = e["code"].(string)
		}
	}
	return &RejoinRefused{Status: status, Code: p.Code, Detail: p.Detail}
}

func joinKey(shared, nodePub, homePub []byte, info string, n int) ([]byte, error) {
	salt := append(append([]byte{}, nodePub...), homePub...)
	return hkdf.Key(sha256.New, shared, salt, info, n)
}

// joinSAS is the six-digit pairing code both sides derive.
func joinSAS(shared, nodePub, homePub []byte) (string, error) {
	okm, err := joinKey(shared, nodePub, homePub, joinSASInfo, 4)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(okm)%1_000_000), nil
}

// openSealed opens the home's envelope (ChaCha20-Poly1305, the node id as its data).
func openSealed(key []byte, nodeID string, nonce, ct []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("bad nonce")
	}
	return aead.Open(nil, nonce, ct, []byte(strings.ToLower(nodeID)))
}

// FormatCode is a pairing code as it is shown ("761 353").
func FormatCode(code string) string {
	if len(code) != 6 {
		return code
	}
	return code[:3] + " " + code[3:]
}
