package directaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Endpoints is the home's direct listener as reported to the cloud (META_ENDPOINTS).
type Endpoints struct {
	Port     int      `json:"port"`
	State    string   `json:"state"` // on, off, cgnat, mapping_failed
	LAN      []string `json:"lan,omitempty"`
	External string   `json:"external,omitempty"`
}

// Check is the outside check's result as the cloud sent it.
type Check struct {
	Result string    `json:"result"` // ok, closed, timeout, wrong_home, cgnat
	Port   int       `json:"port"`
	At     time.Time `json:"at"`
}

// AgentStatus is what the relay agent last heard from the cloud (GET /admin/cloud-link/direct).
type AgentStatus struct {
	Online  bool   `json:"online"`
	Domain  string `json:"domain"`
	HomeID  string `json:"home_id"`
	Chain   string `json:"chain"`
	Check   *Check `json:"check"`
	Pending bool   `json:"pending"`
}

// AgentRequest is what the home tells the cloud through the agent.
type AgentRequest struct {
	Endpoints *Endpoints `json:"endpoints,omitempty"`
	AskName   bool       `json:"ask_name,omitempty"`
	CSR       []byte     `json:"csr,omitempty"`
	CheckNow  bool       `json:"check_now,omitempty"`
}

// Agent is the relay agent's loopback admin port (MAKTABA_CLOUDLINK_ADMIN).
type Agent struct {
	Addr   string
	Client *http.Client
}

func (a *Agent) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Status reads what the cloud last said.
func (a *Agent) Status(ctx context.Context) (AgentStatus, error) {
	var st AgentStatus
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+a.Addr+"/admin/cloud-link/direct", nil)
	if err != nil {
		return st, err
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return st, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return st, fmt.Errorf("the relay agent answered %d (an agent without direct access?)", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&st)
	return st, err
}

// Post tells the cloud r.
func (a *Agent) Post(ctx context.Context, r AgentRequest) error {
	b, _ := json.Marshal(r)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+a.Addr+"/admin/cloud-link/direct", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the relay agent answered %d", resp.StatusCode)
	}
	return nil
}
