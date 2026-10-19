package main

// `maktaba-api cloudflare spike`: the Epic 32 story 32.6 spike (AC7)
// against a real Cloudflare account. Credentials come from the
// environment only (MAKTABA_CF_ACCOUNT_ID, MAKTABA_CF_API_TOKEN or
// MAKTABA_CF_API_TOKEN_FILE); see tools/tv/cf-spike.md.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/spike"
)

func runCloudflare(args []string) error {
	if len(args) == 0 || args[0] != "spike" {
		return errors.New("usage: maktaba-api cloudflare spike [flags] (see -h)")
	}
	return runSpike(args[1:], os.Getenv, os.Stdout, os.Stderr)
}

func runSpike(args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("cloudflare spike", flag.ContinueOnError)
	fs.SetOutput(stderr)
	code := fs.String("customer-code", getenv("MAKTABA_CF_CUSTOMER_CODE"), "customer subdomain code (customer-<code>.cloudflarestream.com); learned from the broadcast when empty")
	ingest := fs.String("ingest", "rtmps", "rtmps or srt")
	duration := fs.Duration("duration", 60*time.Second, "length of the test broadcast")
	drops := fs.String("drops", "", "comma-separated ingest drops to force, e.g. 10s,60s (check d)")
	timeoutSec := fs.Int("timeout-seconds", 30, "the input's recording.timeoutSeconds")
	lowLatency := fs.Bool("low-latency", false, "preferLowLatency on the input (LL-HLS, check c)")
	expTTL := fs.Duration("exp-ttl", 40*time.Second, "token life for the per-segment exp check (b)")
	limit := fs.Int("limit-probe", 0, "create up to N extra inputs to find the account's limit (f); 0 skips")
	outURL := fs.String("output-url", "", "RTMP(S) URL of a live output to attach (g); its key from MAKTABA_CF_OUTPUT_KEY")
	keep := fs.Bool("keep", false, "keep the input, recording and signing key")
	ffmpeg := fs.String("ffmpeg", getenv("MAKTABA_FFMPEG"), "ffmpeg binary (default ffmpeg on PATH)")
	source := fs.String("source", "", "push this MPEG-TS file (looped) instead of a test pattern")
	clock := fs.Bool("clock", false, "burn the wall clock into the picture (read glass-to-glass latency by eye)")
	out := fs.String("out", "", "also write the JSON report to this file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	account, token, err := spike.Env(getenv)
	if err != nil {
		return err
	}
	var dropList []time.Duration
	for _, d := range strings.Split(*drops, ",") {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		v, err := time.ParseDuration(d)
		if err != nil {
			return fmt.Errorf("-drops: %w", err)
		}
		dropList = append(dropList, v)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	rep, runErr := spike.Run(ctx, spike.Options{
		Client: cf.New(account, token), CustomerCode: *code, Ingest: *ingest, Duration: *duration,
		Drops: dropList, TimeoutSeconds: *timeoutSec, LowLatency: *lowLatency, ExpTTL: *expTTL,
		LimitProbe: *limit, OutputURL: *outURL, OutputKey: getenv("MAKTABA_CF_OUTPUT_KEY"), Keep: *keep,
		Pusher: spike.FFmpegPusher{Bin: *ffmpeg, Source: *source, Clock: *clock, Log: stderr},
		Log:    stderr,
	})
	b, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Fprintln(stdout, string(b))
	if *out != "" {
		if err := os.WriteFile(*out, append(b, '\n'), 0o600); err != nil {
			return err
		}
	}
	return runErr
}
