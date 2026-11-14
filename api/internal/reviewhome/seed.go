package reviewhome

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/refresh"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// The accounts' default names (the submission notes print the viewer's; docs/stores/review-home.md).
const (
	DefaultViewer = "reviewer"
	DefaultChild  = "reviewer-child"
)

// MinPasswordLen is the shortest password Seed and Rotate take: the account is on the
// public internet and its name is printed in every store's submission notes.
const MinPasswordLen = 16

// ChildTier is the restricted child's limit: 9+, unrated hidden, so the older viewers'
// library (13+) is out of its reach and a reviewer sees the blocked page.
const ChildTier = parental.Tier9

// Accounts are the two accounts and their passwords (from the release secrets).
type Accounts struct {
	Viewer, ViewerPassword string
	Child, ChildPassword   string
}

// Check refuses a password that is short, equal to the other one or holds the username.
func (a Accounts) Check() error {
	var errs []string
	for _, p := range [][2]string{{a.Viewer, a.ViewerPassword}, {a.Child, a.ChildPassword}} {
		switch {
		case p[0] == "":
			errs = append(errs, "an account has no username")
		case utf8.RuneCountInString(p[1]) < MinPasswordLen:
			errs = append(errs, fmt.Sprintf("%s: the password is shorter than %d characters", p[0], MinPasswordLen))
		case strings.Contains(strings.ToLower(p[1]), strings.ToLower(p[0])):
			errs = append(errs, fmt.Sprintf("%s: the password holds the username", p[0]))
		}
	}
	if strings.EqualFold(a.Viewer, a.Child) {
		errs = append(errs, "the viewer and the child need different usernames")
	}
	if a.ViewerPassword != "" && a.ViewerPassword == a.ChildPassword {
		errs = append(errs, "the viewer and the child need different passwords")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Report is what Seed did, for the operator.
type Report struct {
	ViewerID, ChildID string
	Libraries         map[string]string // manifest id -> library id
	ChannelID         string
	Titled            int
	// Missing are the items not scanned yet: run Seed again after the libraries' scan.
	Missing []string
}

// ErrAdmin: an account of that name exists and is an admin. Seed never demotes or uses one:
// the reviewer must be a viewer, and an admin of that name is someone's mistake to look at.
var ErrAdmin = errors.New("reviewhome: an account of that name is an admin")

// Seed makes the review home's libraries, accounts, limits, channel and titles. It can run
// any number of times; each run sets both passwords to the ones given.
func Seed(ctx context.Context, db *sql.DB, m *Manifest, a Accounts) (*Report, error) {
	if err := a.Check(); err != nil {
		return nil, err
	}
	rep := &Report{Libraries: map[string]string{}}
	for _, l := range m.Libraries {
		id, err := ensureLibrary(ctx, db, m, l)
		if err != nil {
			return nil, fmt.Errorf("library %s: %w", l.ID, err)
		}
		rep.Libraries[l.ID] = id
	}
	us := users.New(db)
	var err error
	if rep.ViewerID, err = ensureUser(ctx, us, a.Viewer, a.ViewerPassword); err != nil {
		return nil, fmt.Errorf("viewer: %w", err)
	}
	if rep.ChildID, err = ensureUser(ctx, us, a.Child, a.ChildPassword); err != nil {
		return nil, fmt.Errorf("child: %w", err)
	}
	for _, id := range rep.Libraries {
		for _, u := range []string{rep.ViewerID, rep.ChildID} {
			if _, err := db.ExecContext(ctx,
				`INSERT INTO library_acl (user_id, library_id) VALUES ($1, $2) ON CONFLICT (user_id, library_id) DO NOTHING`,
				u, id); err != nil {
				return nil, fmt.Errorf("library access: %w", err)
			}
		}
	}
	ps := parental.NewStore(db)
	// the viewer shows everything: no limit of its own (a guardian has none)
	if err := ps.Lift(ctx, "", rep.ViewerID); err != nil && !errors.Is(err, parental.ErrNotRestricted) {
		return nil, fmt.Errorf("viewer limit: %w", err)
	}
	if err := restrict(ctx, ps, rep.ChildID); err != nil {
		return nil, fmt.Errorf("child limit: %w", err)
	}
	// the reviewer looks after the child, so Ask a parent can be answered from the viewer
	if err := ps.AddGuardian(ctx, "", rep.ChildID, rep.ViewerID); err != nil {
		return nil, fmt.Errorf("guardian: %w", err)
	}
	if rep.ChannelID, err = ensureChannel(ctx, db, m, rep.Libraries[m.Channel.Library]); err != nil {
		return nil, fmt.Errorf("channel: %w", err)
	}
	for _, it := range m.Items {
		n, err := titleItem(ctx, db, rep.Libraries[it.Library], it)
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", it.ID, err)
		}
		if n == 0 {
			rep.Missing = append(rep.Missing, it.ID)
		}
		rep.Titled += n
	}
	return rep, nil
}

// Rotate sets both accounts' new passwords and signs out every device and browser they
// are signed in on (the TVs a store's reviewers paired included). The accounts must exist.
func Rotate(ctx context.Context, db *sql.DB, a Accounts) error {
	if err := a.Check(); err != nil {
		return err
	}
	us := users.New(db)
	pairs := [][2]string{{a.Viewer, a.ViewerPassword}, {a.Child, a.ChildPassword}}
	// both accounts are looked up before either password changes: a missing or admin account
	// leaves both passwords as they were, never one rotated and one not
	ids := make([]string, len(pairs))
	for i, p := range pairs {
		u, err := us.GetByUsername(ctx, p[0])
		if err != nil {
			return fmt.Errorf("%s: %w", p[0], err)
		}
		if u.IsAdmin {
			return fmt.Errorf("%s: %w", p[0], ErrAdmin)
		}
		ids[i] = u.ID
	}
	for i, p := range pairs {
		pw, no := p[1], false
		if _, err := us.Update(ctx, ids[i], users.UpdateInput{Password: &pw, MustChangePassword: &no}); err != nil {
			return fmt.Errorf("%s: %w", p[0], err)
		}
		if _, err := sessions.New(db).RevokeAllForUser(ctx, ids[i]); err != nil {
			return fmt.Errorf("%s: sign out browsers: %w", p[0], err)
		}
		if _, err := refresh.New(db).RevokeAllForUser(ctx, ids[i]); err != nil {
			return fmt.Errorf("%s: sign out devices: %w", p[0], err)
		}
	}
	return nil
}

// ensureLibrary finds the library by its folder (or its name) and makes it when missing,
// with the manifest's name and age tier. A changed tier asks the recompute for its videos.
func ensureLibrary(ctx context.Context, db *sql.DB, m *Manifest, l ManifestLibrary) (string, error) {
	root := m.Root(l)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	var tier sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT id::text, age_tier FROM libraries WHERE $1 = ANY(roots) OR name = $2 ORDER BY ($1 = ANY(roots)) DESC LIMIT 1`,
		root, l.Name()).Scan(&id, &tier)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO libraries (name, roots, age_tier) VALUES ($1, $2, $3) RETURNING id::text`,
			l.Name(), pq.Array([]string{root}), l.AgeTier).Scan(&id); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	case !tier.Valid || int(tier.Int64) != l.AgeTier:
		if _, err := tx.ExecContext(ctx, `UPDATE libraries SET age_tier = $2, updated_at = now() WHERE id = $1`, id, l.AgeTier); err != nil {
			return "", err
		}
	default:
		return id, tx.Commit()
	}
	// the home's recompute keeps videos.age_tier; it polls this queue
	if _, err := tx.ExecContext(ctx, `INSERT INTO parental_recompute_jobs (kind, subject) VALUES ('library', $1)`, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// ensureUser makes a viewer of that name, or sets the password of the viewer that exists.
func ensureUser(ctx context.Context, us *users.Store, name, password string) (string, error) {
	u, err := us.GetByUsername(ctx, name)
	if errors.Is(err, users.ErrNotFound) {
		u, err = us.Create(ctx, users.CreateInput{Username: name, Password: password, IsAdmin: false})
		if err != nil {
			return "", err
		}
		return u.ID, nil
	}
	if err != nil {
		return "", err
	}
	if u.IsAdmin {
		return "", ErrAdmin
	}
	no := false
	if _, err := us.Update(ctx, u.ID, users.UpdateInput{Password: &password, MustChangePassword: &no}); err != nil {
		return "", err
	}
	return u.ID, nil
}

// restrict gives the child its limit, or puts it back when someone changed it.
func restrict(ctx context.Context, ps *parental.Store, child string) error {
	in := parental.PolicyInput{MaxTier: ChildTier, Unrated: parental.UnratedHide}
	p, err := ps.Policy(ctx, child)
	switch {
	case errors.Is(err, parental.ErrNotRestricted):
		_, err = ps.SetPolicy(ctx, "", child, in)
		return err
	case err != nil:
		return err
	case p.MaxTier == in.MaxTier && p.Unrated == in.Unrated && !p.AllowOnly:
		return nil
	}
	in.Version = p.Version
	_, err = ps.SetPolicy(ctx, "", child, in)
	return err
}

// ensureChannel makes the live channel, a shuffle of one library, when no channel of that
// library has its name.
func ensureChannel(ctx context.Context, db *sql.DB, m *Manifest, libraryID string) (string, error) {
	name := Both(m.Channel.NameEN, m.Channel.NameAR)
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT id::text FROM channels WHERE library_id = $1 AND name = $2 AND lifecycle = 'live'`, libraryID, name).Scan(&id)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO channels (library_id, number, name, slug, mode) VALUES ($1, $2, $3, 'review', 'shuffle') RETURNING id::text`,
		libraryID, m.Channel.Number, name).Scan(&id); err != nil {
		return "", err
	}
	// the scheduler builds a stale channel's programmes on its next pass
	_, err = db.ExecContext(ctx, `
		INSERT INTO channel_schedule_state (channel_id, stale, sched_gen)
		VALUES ($1, true, COALESCE((SELECT live_gen FROM tv_routing WHERE scope = 'cluster'), 0))
		ON CONFLICT (channel_id, sched_gen) DO UPDATE SET stale = true`, id)
	return id, err
}

// titleItem writes an item's two titles and its licence note on the videos of its library
// with its file name, and answers how many it found (0: not scanned yet).
func titleItem(ctx context.Context, db *sql.DB, libraryID string, it ManifestItem) (int, error) {
	if _, err := db.ExecContext(ctx,
		`UPDATE videos SET title = $3, description = $4, updated_at = now()
		  WHERE library_id = $1 AND filename = $2 AND (title IS DISTINCT FROM $3 OR description IS DISTINCT FROM $4)`,
		libraryID, it.File, it.Title(), it.LicenceNote()); err != nil {
		return 0, err
	}
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM videos WHERE library_id = $1 AND filename = $2`, libraryID, it.File).Scan(&n)
	return n, err
}
