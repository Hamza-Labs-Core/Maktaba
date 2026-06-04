package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/models"
)

func newModelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Manage Whisper transcription models",
	}
	cmd.AddCommand(newModelsListCmd(), newModelsDownloadCmd(), newModelsActiveCmd())
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
				if models.Installed(cfg.Server.DataDir, m.Name) {
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
			fmt.Printf("Downloading %s into %s ...\n", name, models.Dir(cfg.Server.DataDir))
			if err := models.Download(cfg.Server.DataDir, name); err != nil {
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
			fmt.Printf("dir:     %s\n", models.Dir(cfg.Server.DataDir))
			return nil
		},
	}
}
