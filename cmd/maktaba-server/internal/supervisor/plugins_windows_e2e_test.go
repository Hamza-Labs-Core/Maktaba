//go:build windows

package supervisor

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pluginrelay"
)

// #710, end to end on Windows: the plugins role as serve builds it starts the real
// maktaba-plugind inside its AppContainer through the launcher (MAKTABA_PLUGIND_BIN and
// MAKTABA_PLUGINS_SANDBOX_RUN: CI's release build and aclaunch), the host answers its health
// on its AF_UNIX socket, and the pipeline's way in, the supervisor's relay pipe, carries a
// health frame to it and its answer back. MAKTABA_PLUGINS_E2E_REQUIRE=1 fails rather than
// skips without the binaries.
func TestThePluginHostOnWindowsThroughTheRelay(t *testing.T) {
	if os.Getenv("MAKTABA_PLUGIND_BIN") == "" || os.Getenv("MAKTABA_PLUGINS_SANDBOX_RUN") == "" {
		if os.Getenv("MAKTABA_PLUGINS_E2E_REQUIRE") == "1" {
			t.Fatal("MAKTABA_PLUGIND_BIN and MAKTABA_PLUGINS_SANDBOX_RUN are required")
		}
		t.Skip("no maktaba-plugind or launcher (MAKTABA_PLUGIND_BIN, MAKTABA_PLUGINS_SANDBOX_RUN)")
	}
	dir, err := os.MkdirTemp("", "mkp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cfg := config.Config{}
	cfg.Server.DataDir = dir
	cfg.Database.URL = "postgres://localhost/maktaba"
	if !PluginsHere(cfg) {
		t.Fatal("a home on Postgres runs plugins on Windows now")
	}
	p, health, err := BuildPlugins(Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if p.BeforeStart != nil {
		p.BeforeStart()
	}
	cmd := exec.Command(p.Cmd.Path, p.Cmd.Args[1:]...)
	cmd.Env, cmd.Stdout, cmd.Stderr = p.Cmd.Env, os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // the Job Object takes the host with the launcher
		_, _ = cmd.Process.Wait()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for {
		if err := health.Check(ctx); err == nil {
			break
		} else if ctx.Err() != nil {
			t.Fatalf("the host never answered on its socket: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	pipe := PluginsPipe(cfg)
	if pipe == "" {
		t.Fatal("the relay did not start")
	}
	c, err := pluginrelay.Dial(pipe)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	one := make([]byte, 1)
	if _, err := io.ReadFull(c, one); err != nil || one[0] != pluginrelay.Ready {
		t.Fatalf("no Ready from the relay: %v %v", one, err)
	}
	body, _ := json.Marshal(map[string]any{"type": "health"})
	head := make([]byte, 4)
	binary.BigEndian.PutUint32(head, uint32(len(body)))
	if _, err := c.Write(append(head, body...)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(c, head); err != nil {
		t.Fatal(err)
	}
	ans := make([]byte, binary.BigEndian.Uint32(head))
	if _, err := io.ReadFull(c, ans); err != nil {
		t.Fatal(err)
	}
	var hh struct {
		Type    string `json:"type"`
		OK      bool   `json:"ok"`
		Sandbox string `json:"sandbox"`
	}
	if err := json.Unmarshal(ans, &hh); err != nil || !hh.OK {
		t.Fatalf("the host's health through the relay: %s (%v)", ans, err)
	}
	t.Logf("through the relay: %s", ans)
}
