package main

import (
	"context"
	"fmt"
	neturl "net/url"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// serveLLM resolves the language model server for a serve of the whole machine:
// the Ollama bundled with Maktaba.app (its port claimed, its URL in cfg for
// every role) or [llm] url. It returns the ollama role's process (nil without a
// bundled server) and the supervisor option that serves the server's state
// (nil without any server), and starts the watcher that downloads a missing
// model.
func serveLLM(ctx context.Context, cfg *config.Config, whole bool) (*supervisor.Process, supervisor.Option, error) {
	if !whole {
		return nil, nil, nil
	}
	exe, _ := os.Executable()
	srv, bundled, err := llmserver.Resolve(*cfg, exe)
	if err != nil {
		return nil, nil, err
	}
	var proc *supervisor.Process
	if bundled {
		if err := srv.Claim(llmserver.CanBind); err != nil {
			return nil, nil, err
		}
		if srv.Port != llmserver.DefaultPort && !srv.PortSet {
			fmt.Fprintf(os.Stderr, "[llm] port %d is taken: the model server listens on %d\n", llmserver.DefaultPort, srv.Port)
		}
		p, err := supervisor.BuildOllama(srv)
		if err != nil {
			return nil, nil, err
		}
		proc = &p
		cfg.LLM.RuntimeURL = srv.URL()
	}
	url := supervisor.LLMURL(*cfg)
	if url == "" {
		return proc, nil, nil
	}
	mgr := llmserver.NewManager(url, llmserver.Models(*cfg), bundled && srv.Pull, bundled, os.Stderr)
	go mgr.Run(ctx)
	fmt.Fprintf(os.Stderr, "[llm] language model server %s (models: %v)\n", llmLogURL(url), mgr.Models)
	return proc, supervisor.WithLLM(mgr.Status), nil
}

// llmLogURL is [llm] url for the log: without its userinfo or query (a remote server's
// login, an api_key=…), which the log must not keep.
func llmLogURL(s string) string {
	u, err := neturl.Parse(s)
	if err != nil {
		return noCreds(s)
	}
	login := u.User != nil
	u.User, u.Fragment = nil, ""
	if u.RawQuery != "" {
		u.RawQuery = "…"
	}
	out := u.String()
	if login {
		out = strings.Replace(out, "://", "://…@", 1)
	}
	return out
}
