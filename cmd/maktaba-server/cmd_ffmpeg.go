package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/ffmpeg"
)

// Epic 39: the Linux app carries no ffmpeg; the roles run the distribution's
// (or the one [media] ffmpeg names). Setup's check, `maktaba-server ffmpeg` and
// the supervisor's status say whether it does what Maktaba needs, and on Linux
// the command that installs the one it needs. The Mac and Windows apps carry
// their own, checked when they are built.

// ffmpegCheck checks the tools the roles would run (tests replace it).
var ffmpegCheck = ffmpeg.Check

// ffmpegInCheck: setup's check looks at ffmpeg (Linux; tests set it).
var ffmpegInCheck = runtime.GOOS == "linux"

// roleFFmpeg checks the ffmpeg and ffprobe the roles of cfg would run.
func roleFFmpeg(ctx context.Context, cfg config.Config) ffmpeg.Report {
	exe, _ := os.Executable()
	return ffmpegCheck(ctx, ffmpeg.Find(cfg.Media, config.AppPayload(exe)))
}

// checkFFmpeg adds setup's ffmpeg findings: its problem and the command that
// fixes it are problems (Next waits for them), what a feature beside the
// library lacks are warnings.
func checkFFmpeg(ctx context.Context, cfg config.Config, res *checkResult) {
	r := roleFFmpeg(ctx, cfg)
	for _, n := range []*ffmpeg.Note{r.Problem, r.Install} {
		if n != nil {
			res.Problems = append(res.Problems, checkNote(*n))
			res.OK = false
		}
	}
	for _, w := range r.Warnings {
		res.Warnings = append(res.Warnings, checkNote(w))
	}
}

// errFFmpeg: the ffmpeg found does not do what Maktaba needs (exit 1).
var errFFmpeg = errors.New("this ffmpeg does not do what Maktaba needs")

func newFFmpegCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "ffmpeg",
		Short: "Check the ffmpeg every role runs, and how to install the one Maktaba needs",
		Long: `ffmpeg says which ffmpeg and ffprobe the roles run ([media] ffmpeg / ffprobe in
server.toml, else MAKTABA_FFMPEG / MAKTABA_FFPROBE, else the desktop app's own,
else the PATH's: on Linux the distribution's) and whether they do what Maktaba
needs: FFmpeg 4.4 or later with libx264, AAC and the filters and formats the TV
and the pipeline use. When they do not, it names what is wrong and, on Linux,
the command that installs the distribution's ffmpeg (RPM Fusion's on Fedora and
RHEL, Packman's on openSUSE). Exit 1 then.

  problem <code> <what is wrong>
  install <the command>
  ffmpeg <path>  ffprobe <path>  source config|env|app|path  version <its own>
  gpu <the GPU H.264 encoders it has>
  warning <code> <what a feature beside the library lacks>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			r := roleFFmpeg(cmd.Context(), cfg)
			out := cmd.OutOrStdout()
			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(r); err != nil {
					return err
				}
			} else {
				printFFmpeg(out, r)
			}
			if !r.OK {
				cmd.SilenceErrors = jsonOut
				return errFFmpeg
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "the report as JSON")
	return cmd
}

func printFFmpeg(w io.Writer, r ffmpeg.Report) {
	if r.OK {
		fmt.Fprintln(w, "ok "+r.Summary())
	} else {
		fmt.Fprintln(w, "problem "+r.Problem.Code+" "+r.Problem.Message)
		if r.Install != nil {
			if c, ok := r.Install.Params["command"].(string); ok && c != "" {
				fmt.Fprintln(w, "install "+c)
			} else {
				fmt.Fprintln(w, "advice "+r.Install.Message)
			}
		}
	}
	for _, kv := range [][2]string{{"ffmpeg", r.FFmpeg}, {"ffprobe", r.FFprobe}, {"source", string(r.Source)},
		{"version", r.Version}, {"gpu", strings.Join(r.HWEncoders, " ")}} {
		if kv[1] != "" {
			fmt.Fprintln(w, kv[0]+" "+kv[1])
		}
	}
	for _, n := range r.Warnings {
		fmt.Fprintln(w, "warning "+n.Code+" "+n.Message)
	}
}
