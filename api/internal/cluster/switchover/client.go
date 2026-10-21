package switchover

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// SupervisorClient is the supervisor's loopback switchover endpoints
// (MAKTABA_SUPERVISOR_URL, the per-boot token in MAKTABA_SUPERVISOR_TOKEN_FILE).
type SupervisorClient struct {
	URL       string
	TokenFile string
	HTTP      *http.Client
}

// SupervisorFromEnv is the supervisor this API runs under (nil: none).
func SupervisorFromEnv() *SupervisorClient {
	u := strings.TrimRight(os.Getenv("MAKTABA_SUPERVISOR_URL"), "/")
	tf := os.Getenv("MAKTABA_SUPERVISOR_TOKEN_FILE")
	if u == "" || tf == "" {
		return nil
	}
	return &SupervisorClient{URL: u, TokenFile: tf, HTTP: &http.Client{Timeout: 15 * time.Minute}}
}

// Call posts req to POST <url>/switchover/<step> and decodes the answer into out.
func (c *SupervisorClient) Call(ctx context.Context, step string, req, out any) error {
	if c == nil {
		return errors.New("this API runs under no supervisor")
	}
	tok, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return fmt.Errorf("the supervisor's token: %w", err)
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/switchover/"+step, bytes.NewReader(b))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the supervisor: %s", strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// maxChunk bounds a sealed chunk (cmd/maktaba-server/internal/switchover: 1 MiB and its
// tag).
const maxChunk = 1<<20 + 16

// readChunks reads the sealed kit's records (index, last, length, the sealed bytes:
// switchover.WriteChunk in the supervisor).
func readChunks(r io.Reader, fn func(i uint64, last bool, sealed []byte) error) error {
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
		if size > maxChunk {
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
