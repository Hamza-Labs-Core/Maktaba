package llmserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Errors of Setup (the setup step's codes: llm_unavailable, disk_full).
var (
	ErrUnavailable = errors.New("no language model server: this install has no bundled Ollama and [llm] url is not set")
	ErrDiskFull    = errors.New("not enough free disk space for the language model")
)

// freeBytes is FreeBytes (tests replace it).
var freeBytes = FreeBytes

// Event is one step of Setup's progress.
type Event struct {
	Stage   string // check, start, download
	Message string
	Done    int64
	Total   int64
}

// Result is what Setup did (the setup step's result).
type Result struct {
	URL     string        `json:"url"`
	Bundled bool          `json:"bundled"`
	Models  []ResultModel `json:"models"`
	// DiskNeeded is what the missing models take (bytes; 0 when none are
	// missing), DiskFree the space free where they go (-1 unknown, or another
	// machine's server).
	DiskNeeded int64 `json:"disk_needed_bytes"`
	DiskFree   int64 `json:"disk_free_bytes"`
}

// ResultModel is one model after Setup.
type ResultModel struct {
	Name       string `json:"name"`
	Ready      bool   `json:"ready"`
	Downloaded bool   `json:"downloaded"` // this run downloaded it
	SizeBytes  int64  `json:"size_bytes,omitempty"`
}

// SetupOptions configures Setup.
type SetupOptions struct {
	Config config.Config
	Exe    string   // the running maktaba-server (where the payload is)
	Models []string // empty: the configured ones
	// SupervisorURL is the running supervisor's admin URL ("" none): when it
	// runs the model server, Setup uses that one.
	SupervisorURL string
	// CheckOnly reports what is missing and the space it needs, downloading
	// nothing.
	CheckOnly bool
	Progress  func(Event)
	Log       io.Writer // the temporary server's output
	Registry  string    // tests: replaces DefaultRegistry
	// StartWait bounds how long a temporary server may take to answer.
	StartWait time.Duration
}

// Setup makes sure the model server has the models: it finds the server (the
// supervisor's, [llm] url, else the bundled one started for the while), checks
// the space the missing models need against what is free, and downloads them
// with progress. A download that stopped resumes where it was.
func Setup(ctx context.Context, o SetupOptions) (Result, error) {
	progress := o.Progress
	if progress == nil {
		progress = func(Event) {}
	}
	models := o.Models
	if len(models) == 0 {
		models = Models(o.Config)
	}
	srv, bundled, err := Resolve(o.Config, o.Exe)
	if err != nil {
		return Result{}, err
	}
	// [llm] url first: the running supervisor serves it without its login (RedactURL)
	url := ExternalURL(o.Config)
	if url == "" {
		url = runningURL(ctx, o.SupervisorURL)
	}
	switch {
	case url != "":
	case bundled:
		progress(Event{Stage: "start", Message: "Starting the language model server"})
		wait := o.StartWait
		if wait <= 0 {
			wait = 60 * time.Second
		}
		var stop func()
		url, stop, err = StartTemp(ctx, srv, orDiscard(o.Log), wait)
		if err != nil {
			return Result{}, err
		}
		defer stop()
	default:
		return Result{}, ErrUnavailable
	}
	res := Result{URL: url, Bundled: bundled && ExternalURL(o.Config) == "", DiskFree: -1}
	c := NewClient(url)
	c.Registry = o.Registry
	have, err := c.Have(ctx)
	if err != nil {
		return res, fmt.Errorf("the language model server at %s: %w", url, err)
	}
	var missing []int
	for _, m := range models {
		rm := ResultModel{Name: m, Ready: have[Canonical(m)]}
		if !rm.Ready {
			missing = append(missing, len(res.Models))
			if n, err := c.Size(ctx, m); err == nil {
				rm.SizeBytes = n
				res.DiskNeeded += n
			}
		}
		res.Models = append(res.Models, rm)
	}
	if res.Bundled {
		res.DiskFree = freeBytes(srv.Models)
	}
	progress(Event{Stage: "check", Message: spaceMessage(res, len(missing))})
	if res.Bundled && res.DiskFree >= 0 && res.DiskNeeded > res.DiskFree {
		return res, fmt.Errorf("%w: %s needed in %s, %s free", ErrDiskFull,
			HumanBytes(res.DiskNeeded), filepath.Dir(srv.Models), HumanBytes(res.DiskFree))
	}
	if o.CheckOnly {
		return res, nil
	}
	for _, i := range missing {
		rm := &res.Models[i]
		msg := "Downloading " + rm.Name
		last := Event{Stage: "download", Message: msg, Total: rm.SizeBytes}
		progress(last)
		err := c.Pull(ctx, rm.Name, func(p Progress) {
			ev := Event{Stage: "download", Message: msg, Done: p.Done, Total: max(p.Total, rm.SizeBytes)}
			if ev != last { // the server repeats itself (verifying, writing the manifest)
				last = ev
				progress(ev)
			}
		})
		if err != nil {
			return res, err
		}
		// the small layers (template, licence) are never reported: done is all of it
		if last.Done != last.Total {
			progress(Event{Stage: "download", Message: msg, Done: last.Total, Total: last.Total})
		}
		rm.Ready, rm.Downloaded = true, true
	}
	return res, nil
}

func spaceMessage(r Result, missing int) string {
	if missing == 0 {
		return "The language model is ready"
	}
	need := "an unknown amount"
	if r.DiskNeeded > 0 {
		need = HumanBytes(r.DiskNeeded)
	}
	msg := fmt.Sprintf("The language model needs %s of disk space", need)
	if r.DiskFree >= 0 {
		msg += fmt.Sprintf(" (%s free)", HumanBytes(r.DiskFree))
	}
	return msg
}

// runningURL is the model server the running supervisor reports (GET /llm),
// when it is up.
func runningURL(ctx context.Context, supervisorURL string) string {
	if supervisorURL == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(supervisorURL, "/")+"/llm", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var s Status
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&s) != nil || !s.Up {
		return ""
	}
	return s.URL
}

// HumanBytes is n as "4.7 GB".
func HumanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
