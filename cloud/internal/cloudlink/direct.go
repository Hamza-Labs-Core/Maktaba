package cloudlink

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

// Direct remote access's frames (story 50.15), re-exported from the relay package.
const (
	KindMetaEndpoints  = relay.KindMetaEndpoints
	KindDirectCSR      = relay.KindDirectCSR
	KindDirectCert     = relay.KindDirectCert
	KindDirectCheck    = relay.KindDirectCheck
	KindDirectCheckNow = relay.KindDirectCheckNow
	KindDirectName     = relay.KindDirectName
)

type (
	EndpointsPayload = relay.EndpointsPayload
	CertPayload      = relay.CertPayload
	CheckPayload     = relay.CheckPayload
	NamePayload      = relay.NamePayload
)

// Direct is the agent's side of direct remote access (story 50.15). The home's API owns the
// listener, the port mapping and the key; it tells the agent what to say to the cloud (POST
// /admin/cloud-link/direct) and reads what the cloud said back (GET). The agent keeps the last
// of each, sends them again on every new tunnel, and holds no key.
type Direct struct {
	mu        sync.Mutex
	endpoints *EndpointsPayload
	askName   bool
	csr       []byte // sent, waiting for its chain
	name      NamePayload
	chain     string
	chainAt   time.Time
	check     *CheckPayload
	send      func(Frame) error // the live tunnel's writer; nil while down or on an older cloud
	gen       int
}

// DirectStatus is what GET /admin/cloud-link/direct answers.
type DirectStatus struct {
	// Online: a tunnel is up and its cloud speaks the direct frames.
	Online bool          `json:"online"`
	Domain string        `json:"domain,omitempty"`
	HomeID string        `json:"home_id,omitempty"`
	Chain  string        `json:"chain,omitempty"`
	At     time.Time     `json:"chain_at,omitzero"`
	Check  *CheckPayload `json:"check,omitempty"`
	// Pending: a CSR was sent and its chain hasn't come yet.
	Pending bool `json:"pending"`
}

// DirectRequest is what the API posts: any of the fields.
type DirectRequest struct {
	Endpoints *EndpointsPayload `json:"endpoints,omitempty"`
	AskName   bool              `json:"ask_name,omitempty"`
	CSR       []byte            `json:"csr,omitempty"`
	CheckNow  bool              `json:"check_now,omitempty"`
}

// attach makes send the live tunnel's writer and says again everything the cloud should know.
// It returns the attachment's number for detach.
func (d *Direct) attach(send func(Frame) error) int {
	d.mu.Lock()
	d.gen++
	gen := d.gen
	d.send = send
	var out []Frame
	if d.endpoints != nil {
		b, _ := json.Marshal(d.endpoints)
		out = append(out, Frame{Kind: KindMetaEndpoints, Payload: b})
	}
	if d.askName {
		out = append(out, Frame{Kind: KindDirectName})
	}
	if d.csr != nil {
		b, _ := json.Marshal(relay.CSRPayload{CSR: d.csr})
		out = append(out, Frame{Kind: KindDirectCSR, Payload: b})
	}
	d.mu.Unlock()
	for _, f := range out {
		if send(f) != nil {
			break
		}
	}
	return gen
}

// detach forgets the writer of attachment gen (a newer tunnel's stays).
func (d *Direct) detach(gen int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.gen == gen {
		d.send = nil
	}
}

// onFrame keeps what the cloud sent.
func (d *Direct) onFrame(f Frame) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch f.Kind {
	case KindDirectName:
		var p NamePayload
		if json.Unmarshal(f.Payload, &p) == nil && p.Domain != "" {
			d.name, d.askName = p, false
		}
	case KindDirectCert:
		var p CertPayload
		if json.Unmarshal(f.Payload, &p) == nil && p.Chain != "" {
			d.chain, d.chainAt, d.csr, d.askName = p.Chain, time.Now(), nil, false
			d.name = NamePayload{Domain: p.Domain, HomeID: p.HomeID}
		}
	case KindDirectCheck:
		var p CheckPayload
		if json.Unmarshal(f.Payload, &p) == nil {
			d.check = &p
		}
	}
}

// errDirectOffline: no tunnel to a cloud that speaks the direct frames.
var errDirectOffline = errors.New("cloudlink: no tunnel to a cloud with direct access")

// Post keeps r and says it to the cloud now if a tunnel is up (or on the next one).
func (d *Direct) Post(r DirectRequest) error {
	d.mu.Lock()
	var out []Frame
	if r.Endpoints != nil {
		p := *r.Endpoints
		d.endpoints = &p
		b, _ := json.Marshal(p)
		out = append(out, Frame{Kind: KindMetaEndpoints, Payload: b})
	}
	if r.AskName {
		d.askName = true
		out = append(out, Frame{Kind: KindDirectName})
	}
	if len(r.CSR) > 0 {
		d.csr = append([]byte(nil), r.CSR...)
		b, _ := json.Marshal(relay.CSRPayload{CSR: d.csr})
		out = append(out, Frame{Kind: KindDirectCSR, Payload: b})
	}
	if r.CheckNow {
		out = append(out, Frame{Kind: KindDirectCheckNow})
	}
	send := d.send
	d.mu.Unlock()
	if send == nil {
		if r.CheckNow {
			return errDirectOffline
		}
		return nil
	}
	for _, f := range out {
		if err := send(f); err != nil {
			return err
		}
	}
	return nil
}

// Status is what the cloud last said.
func (d *Direct) Status() DirectStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := DirectStatus{Online: d.send != nil, Domain: d.name.Domain, HomeID: d.name.HomeID,
		Chain: d.chain, At: d.chainAt, Pending: d.csr != nil}
	if d.check != nil {
		c := *d.check
		st.Check = &c
	}
	return st
}

// DirectHandler serves /admin/cloud-link/direct on loopback: GET what the cloud said, POST a
// DirectRequest. The chain and the check result are public facts about this home; the key
// never passes here.
func DirectHandler(d *Direct) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(d.Status())
		case http.MethodPost:
			var req DirectRequest
			if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			if req.Endpoints != nil && (req.Endpoints.Port < 0 || req.Endpoints.Port > 65535) {
				http.Error(w, "bad port", http.StatusBadRequest)
				return
			}
			if err := d.Post(req); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}
