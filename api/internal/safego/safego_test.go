package safego

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestGoRecoversAndLogsPanic(t *testing.T) {
	var buf syncBuf
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	Go(logger, "test_worker", func() {
		var s []int
		_ = s[len(s)] // an index out of range panics
	})
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(buf.String(), "goroutine_panic") {
		if time.Now().After(deadline) {
			t.Fatal("no goroutine_panic line")
		}
		time.Sleep(time.Millisecond)
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &line); err != nil {
		t.Fatalf("log line is not one JSON object: %v: %q", err, buf.String())
	}
	if line["event"] != "goroutine_panic" || line["goroutine"] != "test_worker" || line["level"] != "ERROR" {
		t.Fatalf("unexpected line: %v", line)
	}
	if s, _ := line["stack"].(string); !strings.Contains(s, "safego") {
		t.Fatalf("stack missing: %v", line["stack"])
	}
	if p, _ := line["panic"].(string); !strings.Contains(p, "index out of range") {
		t.Fatalf("panic value missing: %v", line["panic"])
	}
}

func TestRecoverWithWaitGroupAndNilLogger(_ *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer Recover(nil, "nil_logger")
		panic("boom")
	}()
	wg.Wait() // reaching here means the process survived the panic
}

func TestGoWithoutPanicLogsNothing(t *testing.T) {
	var buf syncBuf
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	done := make(chan struct{})
	Go(logger, "quiet", func() { close(done) })
	<-done
	time.Sleep(10 * time.Millisecond)
	if buf.String() != "" {
		t.Fatalf("logged without a panic: %q", buf.String())
	}
}
