package updater

// Story 51.18: an update carries the packs (specs/epics/51-configurable-components,
// story-51-18-update-rehearsal.md). A pack fits only the server version it was built
// with, so before the switch the packs step fetches and verifies the new release's pack
// of every component installed here from one (and a model revision its index pins anew),
// into a folder of its own beside the running release's: the same staging as the
// release's payload, all or nothing. current is not touched; the new release points it
// at its own folder at boot, a rollback's release at the folder it kept. The commit
// prunes the folders of the releases no longer kept.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// DefaultPacksWait is how long the packs step waits for an install of the same
// component under way here (story 51.16's lock) before it fails components_busy.
const DefaultPacksWait = 10 * time.Minute

// Packs is the machine's packs for its updates.
type Packs struct {
	// M is this machine's packs manager (its folder, catalog, platform, trust root,
	// watermark, Hugging Face cache, [components] packs).
	M *packs.Manager
	// Source is the release source: the packs and the index are fetched with its HTTP
	// client (a node's: the home's mirror first, then GitHub), from the job's asset URLs,
	// and verified here as the release is.
	Source Source
	// Wait bounds the wait for an install under way (0: DefaultPacksWait).
	Wait time.Duration
}

// source is the job's release files over the source's client (no overall timeout: a
// pack is gigabytes; a cut download resumes).
func (p *Packs) source(j *Journal) packs.Source {
	c := &http.Client{}
	if p.Source.HTTP != nil {
		c.Transport = p.Source.HTTP.Transport
	}
	n := packs.NewNet("", c)
	n.URLs = map[string]string{}
	for k, v := range j.Job.Assets {
		n.URLs[k] = v
	}
	if base, ok := p.M.Net.(*packs.Net); ok && base != nil {
		n.HF, n.OllamaRegistry, n.Token = base.HF, base.OllamaRegistry, base.Token
	}
	return n
}

func (p *Packs) plan(ctx context.Context, j *Journal) (*packs.UpdatePlan, error) {
	plan, err := p.M.PlanUpdate(ctx, layout.NormalizeVersion(j.Job.Version), p.source(j))
	if err != nil {
		return nil, packsFailure(err)
	}
	return plan, nil
}

// room is the preflight's part: the room the new release's packs take on their disk
// beside the release's own (disk_space before anything is fetched). Nothing held here:
// nothing fetched, nothing checked.
func (p *Packs) room(ctx context.Context, j *Journal) error {
	if len(p.M.Layout.Held()) == 0 {
		return nil
	}
	plan, err := p.plan(ctx, j)
	if err != nil {
		return err
	}
	if plan.Need == 0 {
		return nil
	}
	return checkRoom(p.M.Layout.Root, uint64(roomRelease+plan.Need+plan.Need/10))
}

// stage is the packs step: it returns the step's detail and the folders made ready.
func (p *Packs) stage(ctx context.Context, j *Journal) (string, []packs.Staged, error) {
	if len(p.M.Layout.Held()) == 0 {
		return "skipped: no packs", nil, nil
	}
	plan, err := p.plan(ctx, j)
	if err != nil {
		return "", nil, err
	}
	wait := p.Wait
	if wait <= 0 {
		wait = DefaultPacksWait
	}
	sctx, cancel := context.WithTimeout(ctx, wait+time.Duration(plan.Need/(1<<20))*time.Second)
	defer cancel()
	staged, err := p.M.StageUpdate(sctx, plan, p.source(j))
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		return "", nil, packsFailure(err)
	}
	var parts []string
	for _, s := range staged {
		parts = append(parts, s.ID+" "+s.Dir)
	}
	return strings.Join(parts, ", "), staged, nil
}

// packsFailure maps a packs error to the run's failure code (updates.code.<code>).
func packsFailure(err error) error {
	var f *Failure
	if errors.As(err, &f) {
		return err
	}
	code := "packs_failed"
	switch packs.ReasonOf(err) {
	case packs.ReasonVerifyFailed, packs.ReasonUnpackRefused:
		code = "pack_verify_failed"
	case packs.ReasonDownloadFailed, packs.ReasonPackNotPublished:
		code = "pack_download_failed"
	case packs.ReasonBusy:
		code = "components_busy"
	case packs.ReasonIndexMissing, packs.ReasonIndexExpired, packs.ReasonIndexWrongVersion, packs.ReasonIndexReplayed,
		packs.ReasonDiskSpace, packs.ReasonPackMissing, packs.ReasonPacksDisabled:
		code = packs.ReasonOf(err)
	}
	return &Failure{Code: code, Detail: err.Error()}
}

// ---- the machine's side ----

// packsStep is the prepare's packs step (between stage and smoke).
func (m *Machine) packsStep(ctx context.Context) (string, error) {
	j := m.Status().Journal
	detail, staged, err := m.Packs.stage(ctx, j)
	if len(staged) > 0 {
		_ = m.mutate(func(j *Journal) { j.Packs = staged })
	}
	return detail, err
}

// discard removes what a run staged that ended before its switch: the installer's, and
// the folders its packs step made.
func (m *Machine) discard(j *Journal) error {
	err := m.Installer.Discard(context.Background(), j)
	if m.Packs != nil && j != nil && len(j.Packs) > 0 {
		m.Packs.M.DiscardStaged(j.Packs)
	}
	return err
}

// checkKeptPacks runs before a rollback's switch back: a folder the release before reads
// that was deleted by hand is marked (that component reports pack_missing there); the
// rollback goes ahead.
func (m *Machine) checkKeptPacks(j *Journal) {
	if m.Packs == nil || j == nil || j.From == "" || len(j.Packs) == 0 {
		return
	}
	if missing := m.Packs.M.CheckKept(layout.NormalizeVersion(j.From), j.Packs); len(missing) > 0 {
		m.warnf("rolling back to %s without the packs of %s (their folders are gone): they report pack_missing",
			j.From, strings.Join(missing, ", "))
	}
}

// PrunePacks deletes the pack folders of releases this machine no longer keeps (its
// installer's kept releases and the running one stay, and while a run is under way its
// two releases too). The commit runs it, and so does every start (a folder a prune could
// not delete, busy on Windows, goes then). A failure is logged, never more.
func (m *Machine) PrunePacks() { m.prunePacks(false) }

// prunePacks: committing, the run's previous release stays only when the installer keeps it.
func (m *Machine) prunePacks(committing bool) {
	if m.Packs == nil {
		return
	}
	k, ok := m.Installer.(Keeper)
	if !ok {
		return
	}
	keep := []string{layout.NormalizeVersion(m.Installer.Running())}
	for _, r := range k.Kept() {
		keep = append(keep, layout.NormalizeVersion(r.Version))
	}
	if j := m.Status().Journal; !committing && j != nil && !Ended(j.State) {
		keep = append(keep, layout.NormalizeVersion(j.Job.Version), layout.NormalizeVersion(j.From))
	}
	removed, errs := m.Packs.M.Prune(keep)
	for _, err := range errs {
		m.warnf("a pack folder could not be pruned (tried again at the next start): %v", err)
	}
	if len(removed) > 0 {
		m.logf("pruned the packs of releases no longer kept: %s", strings.Join(removed, ", "))
	}
}
