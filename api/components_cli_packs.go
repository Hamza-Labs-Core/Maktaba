package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	componentsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/components"
	catalog "github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// The pack installer from the command line (story 51.16): `components import <file>`
// and `components offline …` ask this machine's supervisor (the one maktaba-server
// names in MAKTABA_SUPERVISOR_URL and MAKTABA_SUPERVISOR_TOKEN_FILE), and record what
// they did in the same audit rows as the admin routes.

// withProgress sets the progress of the assets of views.
func (c *componentsCLI) withProgress(ctx context.Context, views []components.View) {
	if c.packs == nil {
		return
	}
	var ids []string
	for _, v := range views {
		if v.Kind == catalog.KindAsset {
			ids = append(ids, v.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	got := c.packs.ProgressOf(ctx, ids)
	for i := range views {
		views[i].Progress = got[views[i].ID]
	}
}

// progressWords is p in one line.
func progressWords(p *components.InstallProgress) string {
	switch {
	case p.Busy:
		s := fmt.Sprintf("%s %d%%", p.Phase, p.Percent)
		if p.Detail != "" {
			s += " (" + p.Detail + ")"
		}
		return s
	case p.Reason != "":
		return "failed: " + p.Reason
	case p.Installed:
		return "installed"
	}
	return "not installed"
}

func (c *componentsCLI) notRunning() int {
	fmt.Fprintln(c.errOut, "The Maktaba server is not running on this machine (its supervisor does not answer): start it, then try again.")
	return exitComponentsNotRunning
}

// packsFail prints a refusal of the supervisor (exit 1), or that it does not answer (exit 3).
func (c *componentsCLI) packsFail(err error) int {
	var se *componentsh.SupError
	if !errors.As(err, &se) {
		return c.notRunning()
	}
	if se.Reason == componentsh.CodeUnavailable {
		fmt.Fprintf(c.errOut, "components: this machine installs no components (%s)\n", se.Detail)
		return exitComponentsFailed
	}
	fmt.Fprintf(c.errOut, "Refused (%s): %s\n", se.Reason, se.Detail)
	if len(se.Installed) > 0 {
		fmt.Fprintf(c.errOut, "  installed before the failure: %s\n", strings.Join(se.Installed, ", "))
	}
	if len(se.RequiredBy) > 0 {
		fmt.Fprintf(c.errOut, "  Required by: %s\n", strings.Join(se.RequiredBy, ", "))
	}
	return exitComponentsFailed
}

func (c *componentsCLI) actor() components.Actor { return components.Actor{By: c.user, Source: "cli"} }

// importFile installs what the offline file at p holds (a .tar or a folder on this
// machine); what the home wants absent becomes installed (off).
func (c *componentsCLI) importFile(ctx context.Context, p string, yes bool) int {
	abs, err := filepath.Abs(p)
	if err == nil {
		_, err = os.Stat(abs)
	}
	if err != nil {
		fmt.Fprintf(c.errOut, "components import: %v\n", err)
		return exitComponentsFailed
	}
	if c.packs == nil {
		return c.notRunning()
	}
	fmt.Fprintf(c.out, "This installs on this machine what %s holds; each stays off until it is turned on.\n", filepath.Base(abs))
	if !yes {
		if !c.tty {
			fmt.Fprintln(c.errOut, "components: no terminal to ask on: add --yes to import the file")
			return exitComponentsNeedYes
		}
		fmt.Fprint(c.out, "Continue? [y/N] ")
		line, _ := bufio.NewReader(c.in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(c.out, "Nothing changed.")
			return exitComponentsFailed
		}
	}
	ids, ierr := c.packs.ImportPath(ctx, abs)
	if len(ids) > 0 {
		res, err := c.svc.Imported(context.WithoutCancel(ctx), ids, filepath.Base(abs), c.actor())
		if err != nil {
			fmt.Fprintf(c.errOut, "components: the import is on disk, but its record was not written: %v\n", err)
		}
		for _, d := range res.Changed {
			fmt.Fprintf(c.out, "%s: %s (generation %d)\n", d.ID, d.Want, d.Generation)
		}
	}
	if ierr != nil {
		return c.packsFail(ierr)
	}
	if len(ids) == 0 {
		fmt.Fprintln(c.out, "Nothing to install: everything the file holds is installed already.")
		return 0
	}
	fmt.Fprintf(c.out, "Installed: %s\n", strings.Join(ids, ", "))
	return 0
}

// offline makes an offline file (a bundle, the components named, or with no word the
// full set) for --platform (this machine's when not given) and saves it at --out (a
// file or a folder; the current folder when not given).
func (c *componentsCLI) offline(ctx context.Context, words []string) int {
	var req componentsh.OfflineRequest
	switch {
	case len(words) == 1 && (words[0] == "full" || words[0] == "core" || words[0] == "worker"):
		req.Bundle = words[0]
	default:
		req.Components = words
	}
	req.Platform = c.values["platform"]
	if c.packs == nil {
		return c.notRunning()
	}
	name, err := c.packs.MakeOffline(ctx, req)
	if err != nil {
		return c.packsFail(err)
	}
	c.record(ctx, components.ActionOfflineMake, map[string]any{"name": name, "bundle": req.Bundle,
		"components": req.Components, "platform": req.Platform})
	last := ""
	var resp *http.Response
	for {
		r, pending, err := c.packs.FetchOffline(ctx, name, nil)
		if err != nil {
			return c.packsFail(err)
		}
		if pending == nil {
			resp = r
			break
		}
		if w := progressWords(pending); w != last {
			fmt.Fprintf(c.errOut, "Making %s: %s\n", name, w)
			last = w
		}
		c.wait(time.Second)
	}
	defer func() { _ = resp.Body.Close() }()
	out := c.values["out"]
	if out == "" {
		out = name
	} else if st, err := os.Stat(out); err == nil && st.IsDir() {
		out = filepath.Join(out, name)
	}
	n, err := saveFile(out, resp.Body)
	if err != nil {
		fmt.Fprintf(c.errOut, "components offline: %v\n", err)
		return exitComponentsFailed
	}
	c.record(ctx, components.ActionOfflineDownload, map[string]any{"name": name, "saved": out})
	fmt.Fprintf(c.out, "Saved %s (%d MB).\n", out, (n+(1<<20)-1)>>20)
	return 0
}

// saveFile writes r to path through path.partial, so a cut copy never looks whole.
func saveFile(path string, r io.Reader) (int64, error) {
	tmp := path + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return n, err
	}
	return n, nil
}

func (c *componentsCLI) record(ctx context.Context, action string, payload map[string]any) {
	if err := c.svc.Record(ctx, c.actor(), action, "", payload); err != nil {
		fmt.Fprintf(c.errOut, "components: the audit row was not written: %v\n", err)
	}
}
