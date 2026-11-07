package fakehcloud

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type recMover struct {
	mu    sync.Mutex
	moves []int64
}

func (r *recMover) Move(s int64) error {
	r.mu.Lock()
	r.moves = append(r.moves, s)
	r.mu.Unlock()
	return nil
}

// T01 (story 50.06): assign to B: GET shows B after the delay, the alias moved once.
func TestAssignMovesTheIP(t *testing.T) {
	m := &recMover{}
	api := New(m, 150*time.Millisecond)
	api.Token = "t"
	srv := httptest.NewServer(api)
	defer srv.Close()
	do := func(method, path string, body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer t")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	start := time.Now()
	if st, out := do("POST", "/v1/floating_ips/7/actions/assign", map[string]any{"server": 2}); st != 201 {
		t.Fatalf("assign: %d %v", st, out)
	}
	if _, out := do("GET", "/v1/floating_ips/7", nil); out["floating_ip"].(map[string]any)["server"] != nil {
		t.Error("the IP moved before the delay")
	}
	api.Wait()
	if time.Since(start) < 150*time.Millisecond {
		t.Error("delay not honoured")
	}
	if _, out := do("GET", "/v1/floating_ips/7", nil); out["floating_ip"].(map[string]any)["server"] != float64(2) {
		t.Errorf("holder: %v", out)
	}
	if len(m.moves) != 1 || m.moves[0] != 2 {
		t.Errorf("moves %v", m.moves)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/v1/floating_ips/7", nil)
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d", resp.StatusCode)
	}
	if st, _ := do("POST", "/v1/floating_ips/7/actions/assign", map[string]any{}); st != 422 {
		t.Errorf("no server: %d", st)
	}
}
