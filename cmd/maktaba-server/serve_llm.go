package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// serveLLM resolves the language model server for a serve of the whole machine:
// the Ollama bundled with Maktaba.app (its port claimed, its URL in cfg for
// every role) or [llm] url. It returns the ollama role's process (nil without a
// bundled server) and the supervisor option that serves the server's state
// (nil without any server), and starts the watcher that downloads a missing
// model. With dormant (the reconciler runs, story 51.04) a machine whose server.toml
// leaves the server out still builds it, its gate closed, so enabling the llm component
// starts it without a restart; when its binary is not here yet (a Core Mac: the
// runtime.ollama pack brings it), the process looks for it again before each start and
// the reconciler says not_in_payload until it is there.
func serveLLM(ctx context.Context, cfg *config.Config, whole, dormant bool) (*supervisor.Process, supervisor.Option, error) {
	if !whole {
		return nil, nil, nil
	}
	exe, _ := os.Executable()
	srv, bundled, err := llmserver.Resolve(*cfg, exe)
	if err != nil {
		return nil, nil, err
	}
	held := false
	if !bundled && dormant && llmserver.ExternalURL(*cfg) == "" {
		srv, held = llmserver.Planned(*cfg, llmserver.FindBinary(*cfg, exe)), true
	}
	var proc *supervisor.Process
	if bundled || held {
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
		if held {
			heldOllama(&p, *cfg, exe)
		}
		proc = &p
		cfg.LLM.RuntimeURL = srv.URL()
	}
	url := supervisor.LLMURL(*cfg)
	if url == "" {
		return proc, nil, nil
	}
	ours := bundled || held
	mgr := llmserver.NewManager(url, llmserver.Models(*cfg), ours && srv.Pull, ours, os.Stderr)
	go mgr.Run(ctx)
	if held {
		fmt.Fprintf(os.Stderr, "[llm] language model server %s held until the llm component is enabled\n", llmLogURL(url))
	} else {
		fmt.Fprintf(os.Stderr, "[llm] language model server %s (models: %v)\n", llmLogURL(url), mgr.Models)
	}
	return proc, supervisor.WithLLM(mgr.Status), nil
}

// heldOllama makes p the llm component's dormant process: its gate closed, its binary
// looked up again before each start (the runtime.ollama pack's, installed after the
// boot) and present only once it is found.
func heldOllama(p *supervisor.Process, cfg config.Config, exe string) {
	p.Dormant = true
	p.Path = func() string { return llmserver.FindBinary(cfg, exe) }
	p.Present = func() bool { return llmserver.FindBinary(cfg, exe) != "" }
}

// llmLogURL is [llm] url for the log: without its userinfo or query (a remote server's
// login, an api_key=…), which the log must not keep.
func llmLogURL(s string) string { return llmserver.RedactURL(s) }
