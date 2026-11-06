package main

import (
	"bytes"
	"encoding/json"
	"testing"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Epic 53 story 53.01: the cloud's lines carry service, version, env and node like
// the home's, scrubbed with the shared list.
func TestTheCloudLogsTheSharedLine(t *testing.T) {
	t.Setenv("MAKTABA_ENV", "")
	t.Setenv("MAKTABA_NODE_ID", "cloud-a")
	o := logOptions("relay")
	var buf bytes.Buffer
	o.Output = &buf
	mlog.NewLogger(o).Info("tunnel_open", "url", "https://h/x?token=abc")
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not one JSON line: %q", buf.String())
	}
	for k, want := range map[string]any{
		"service": "cloud.relay", "version": Version, "env": "prod", "node": "cloud-a",
		"msg": "tunnel_open", "level": "info", "url": "https://h/x?token=[REDACTED]",
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
}
