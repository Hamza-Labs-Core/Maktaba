package main

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/models"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

func newModelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Manage Whisper transcription models",
	}
	cmd.AddCommand(newModelsListCmd(), newModelsDownloadCmd(), newModelsActiveCmd(), newModelsLLMCmd())
	return cmd
}

// models llm: the language model(s) of the LLM stages, into the model server's
// folder (the same work as the setup step `llm`).
func newModelsLLMCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "llm [model...]",
		Short: "Download the language model(s) the LLM stages use (Ollama; default: [llm] models)",
		Long: `llm downloads the models of the language model server (Maktaba.app's
bundled Ollama, or the one at [llm] url) that it does not have yet, showing the
disk space they need first. A download that stopped resumes where it was.
Without a model name: [llm] models in server.toml (default qwen2.5:7b).`,
		RunE: func(c *cobra.Command, args []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			exe, _ := os.Executable()
			last := ""
			res, err := llmserver.Setup(c.Context(), llmserver.SetupOptions{
				Config: cfg, Exe: exe, Models: args, SupervisorURL: supervisor.StateURL(cfg),
				CheckOnly: check, Log: io.Discard,
				Progress: func(e llmserver.Event) {
					line := e.Message
					if e.Total > 0 {
						line = fmt.Sprintf("%s: %s of %s", e.Message, llmserver.HumanBytes(e.Done), llmserver.HumanBytes(e.Total))
					}
					if line != last {
						fmt.Fprintf(os.Stderr, "\r%-78s", line)
						if e.Stage != "download" {
							fmt.Fprintln(os.Stderr)
						}
						last = line
					}
				},
			})
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			for _, m := range res.Models {
				state := "missing"
				if m.Ready {
					state = "ready"
				}
				fmt.Printf("%s\t%s\n", m.Name, state)
			}
			fmt.Printf("server: %s\n", res.URL)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only show what is missing and the space it needs")
	return cmd
}

func newModelsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show available models and which are installed",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, _, _ := config.Load()
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "NAME\tBACKEND\tINSTALLED\tNOTES")
			for _, m := range models.Catalog() {
				installed := "no"
				if models.Installed(cfg.StateDir(), m.Name) {
					installed = "yes"
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Name, m.Backend, installed, m.Note)
			}
			return tw.Flush()
		},
	}
}

func newModelsDownloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "download <name>",
		Short: "Download a model into the data dir",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, _, _ := config.Load()
			name := args[0]
			fmt.Printf("Downloading %s into %s ...\n", name, models.Dir(cfg.StateDir()))
			if err := models.Download(cfg.StateDir(), name); err != nil {
				return err
			}
			fmt.Printf("Done. Set [transcription].model to use it (see `maktaba-server models active`).\n")
			return nil
		},
	}
}

func newModelsActiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "active",
		Short: "Show the active transcription backend + model",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, _, _ := config.Load()
			fmt.Printf("backend: %s\n", cfg.Transcription.Backend)
			fmt.Printf("model:   %s\n", cfg.Transcription.Model)
			fmt.Printf("dir:     %s\n", models.Dir(cfg.StateDir()))
			return nil
		},
	}
}
