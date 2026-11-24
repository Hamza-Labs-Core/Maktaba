// Command release-feed-check reads GitHub's release feed exactly as a home's update check
// reads it (api/internal/updates: the same URLs, headers, pages and caps, through the same
// code) and fails when a home could not read it, or soon could not:
//
//   - any page of the release list over 70% of its cap (updates.FeedHeadroom);
//   - the newest published stable release (or -expect) not selectable by the home's catalog;
//   - the release-by-tag read of it over 70% of its cap.
//
// It runs hourly (.github/workflows/release-feed.yml, which opens an issue on a failure)
// and in each release workflow once it publishes (tools/release/feed-check.sh). Since
// 2026-11-23 every home's check had failed on a list longer than its cap, for over a day,
// and nothing outside a home read the feed the way a home does.
//
//	go run ./cmd/release-feed-check [-repo owner/name] [-expect vX.Y.Z] [-token-env GITHUB_TOKEN] [-json FILE]
//
// It reads unauthenticated, as a home does. Only when GitHub refuses that for its rate
// limit (GitHub's runners share their addresses) does it read again with the token in
// -token-env, and says so: a token without push access sees the same list.
//
// Exit status: 0 the feed is fine, 1 a problem (each printed, and as a GitHub annotation
// under Actions), 2 a usage error.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
)

func main() {
	repo := flag.String("repo", envOr("GITHUB_REPOSITORY", "Hamza-Labs-Core/Maktaba"), "the repository whose releases homes read")
	expect := flag.String("expect", "", "the tag a home must be able to select (default: GitHub's latest release)")
	api := flag.String("api", "", "the GitHub API base (default https://api.github.com)")
	tokenEnv := flag.String("token-env", "", "an environment variable holding a token, used only after a rate-limited anonymous read")
	jsonOut := flag.String("json", "", "also write the report as JSON to this file")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "release-feed-check: no arguments expected")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &updates.Discoverer{Repo: *repo, BaseURL: *api, Log: quiet}
	rep := d.ProbeFeed(ctx, *expect)
	how := "anonymously, as a home reads it"
	if rep.RateLimited && *tokenEnv != "" && os.Getenv(*tokenEnv) != "" {
		d = &updates.Discoverer{Repo: *repo, BaseURL: *api, Log: quiet, Token: os.Getenv(*tokenEnv)}
		rep = d.ProbeFeed(ctx, *expect)
		how = "with " + *tokenEnv + " (the anonymous read was rate-limited)"
	}

	md := report(rep, how)
	_, _ = fmt.Fprint(os.Stdout, md)
	if f := os.Getenv("GITHUB_STEP_SUMMARY"); f != "" {
		appendFile(f, md)
	}
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*jsonOut, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if rep.OK() {
		return
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		for _, p := range rep.Problems {
			_, _ = fmt.Fprintf(os.Stdout, "::error title=Release feed::%s\n", escape(p))
		}
	}
	os.Exit(1)
}

// report is the check's Markdown: each read with its size against its cap, then what
// the home's catalog can select, then the problems.
func report(rep updates.FeedReport, how string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### The release feed of %s\n\n", rep.Repo)
	fmt.Fprintf(&b, "Read %s. A home may use %.0f%% of each cap.\n\n", how, 100*updates.FeedHeadroom)
	b.WriteString("| Read | Bytes | Cap | Share | Releases | Error |\n|---|---:|---:|---:|---:|---|\n")
	row := func(name string, r updates.FeedRead) {
		fmt.Fprintf(&b, "| %s | %d | %d | %.0f%% | %d | %s |\n", name, r.Bytes, r.Cap, 100*r.Share(), r.Releases, r.Err)
	}
	for i, p := range rep.Pages {
		row(fmt.Sprintf("list page %d", i+1), p)
	}
	if rep.Tag != nil {
		row("release "+rep.Target+" by tag", *rep.Tag)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- GitHub's latest release: %s\n", orNone(rep.Latest))
	fmt.Fprintf(&b, "- Looked for: %s\n", orNone(rep.Target))
	fmt.Fprintf(&b, "- The home's catalog can select: %s\n", orNone(strings.Join(rep.Selectable, ", ")))
	if rep.Drafts > 0 {
		fmt.Fprintf(&b, "- Drafts in the list (a token with push access): %d\n", rep.Drafts)
	}
	if rep.OK() {
		b.WriteString("\nA home reads this feed with room to spare.\n")
		return b.String()
	}
	b.WriteString("\n**A home cannot read this feed, or soon cannot:**\n\n")
	for _, p := range rep.Problems {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// escape keeps an annotation's message on one line (GitHub's workflow command escaping).
func escape(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func appendFile(path, s string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(s)
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
