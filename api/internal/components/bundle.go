package components

import (
	"context"
	"net/http"
	"slices"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Changing bundle after setup (story 51.17 §5): the bundles, which one the home is based
// on, and the difference a bundle makes, applied as one batch of desired-state changes
// through Apply (one transaction, one revision, the audit rows of one batch).

// BundleInfo is one bundle: what it turns on and what it downloads on this platform.
type BundleInfo struct {
	ID            string   `json:"id"`
	Components    []string `json:"components"`
	DownloadBytes int64    `json:"download_bytes"`
}

// BundlesView is GET /api/components/bundles.
type BundlesView struct {
	BasedOn string       `json:"based_on"`
	Set     string       `json:"set"`
	Plus    []string     `json:"plus"`
	Minus   []string     `json:"minus"`
	Bundles []BundleInfo `json:"bundles"`
}

// AssetInstall is one asset a bundle change downloads.
type AssetInstall struct {
	ID    string `json:"id"`
	Bytes int64  `json:"bytes"`
}

// BundleDiff is POST /api/components/bundle: what changing to To does.
type BundleDiff struct {
	BasedOn       string         `json:"based_on"`
	To            string         `json:"to"`
	Enable        []string       `json:"enable"`
	Install       []AssetInstall `json:"install"`
	Disable       []string       `json:"disable"`
	Remove        []string       `json:"remove"`
	DownloadBytes int64          `json:"download_bytes"`
	Revision      int64          `json:"revision"`
	DryRun        bool           `json:"dry_run"`
	Effect        Preview        `json:"effect"`
	// BatchID, Changed: set when it was applied.
	BatchID string          `json:"batch_id,omitempty"`
	Changed []store.Desired `json:"changed,omitempty"`
}

// platform is the home's catalog platform (the API runs on the home).
func (s *Service) platform() string { return components.ThisPlatform() }

// Bundles lists the bundles and the one the home is based on.
func (s *Service) Bundles(ctx context.Context) (BundlesView, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return BundlesView{}, err
	}
	line := s.bundleLine(snap)
	out := BundlesView{BasedOn: line.Base, Set: line.Set, Plus: line.Plus, Minus: line.Minus, Bundles: []BundleInfo{}}
	cat := s.cat()
	for _, b := range components.Bundles {
		info := BundleInfo{ID: b, Components: []string{}}
		for _, id := range cat.InBundle(b) {
			c, _ := cat.Get(id)
			if !c.Switchable || !c.RunsOn(s.platform()) {
				continue
			}
			info.Components = append(info.Components, id)
			if wantOf(snap.rows, id) == components.WantAbsent {
				info.DownloadBytes += c.AssetBytes(s.platform())
			}
		}
		out.Bundles = append(out.Bundles, info)
	}
	return out, nil
}

// diffOf sorts a bundle's changes over rows into what is turned on, downloaded, turned
// off and removed.
func (s *Service) diffOf(rows map[string]store.Desired, bundle string, removeUnused bool) BundleDiff {
	cat := s.cat()
	d := BundleDiff{To: bundle, Enable: []string{}, Install: []AssetInstall{}, Disable: []string{}, Remove: []string{}}
	for _, ch := range bundleChanges(cat, rows, bundle, removeUnused) {
		c, _ := cat.Get(ch.ID)
		was := wantOf(rows, ch.ID)
		switch {
		case ch.Want == components.WantAbsent:
			d.Remove = append(d.Remove, ch.ID)
		case ch.Want == components.WantInstalled:
			d.Disable = append(d.Disable, ch.ID)
		case c.Kind == components.KindAsset:
			if was == components.WantAbsent {
				n := c.AssetBytes(s.platform())
				d.Install = append(d.Install, AssetInstall{ID: ch.ID, Bytes: n})
				d.DownloadBytes += n
			}
		default:
			d.Enable = append(d.Enable, ch.ID)
		}
	}
	return d
}

// Bundle answers what changing to bundle does and, unless dryRun, applies it in one
// batch: a refusal of any change refuses the whole bundle (a licence the home lacks, a
// list that moved on since generation).
func (s *Service) Bundle(ctx context.Context, bundle string, removeUnused, dryRun bool, generation *int64, actor Actor) (BundleDiff, error) {
	if !slices.Contains(components.Bundles, bundle) {
		return BundleDiff{}, refuse(http.StatusBadRequest, "", CodeBadBundle, "no bundle "+bundle+" (full, core or worker)")
	}
	snap, err := s.snapshot(ctx)
	if err != nil {
		return BundleDiff{}, err
	}
	d := s.diffOf(snap.rows, bundle, removeUnused)
	d.BasedOn, d.Revision, d.DryRun = s.bundleLine(snap).Base, snap.rev, dryRun
	gen := snap.rev
	if generation != nil {
		gen = *generation
	}
	req := Request{Bundle: bundle, RemoveUnused: removeUnused, Generation: &gen}
	if dryRun {
		p, err := s.Preview(ctx, req)
		if err != nil {
			return BundleDiff{}, err
		}
		d.Effect = p
		return d, nil
	}
	res, err := s.Apply(ctx, req, actor)
	if err != nil {
		return BundleDiff{}, err
	}
	d.Effect, d.BatchID, d.Changed, d.Revision = res.Effect, res.BatchID, res.Changed, res.Revision
	return d, nil
}
