package parental_test

// The store against a real migrated Postgres (slot 0221). Runs when
// MAKTABA_TEST_PG_URL names a Postgres the user may create databases on.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

const (
	uAdmin    = "00000000-0000-4000-8000-000000570001"
	uGuardian = "00000000-0000-4000-8000-000000570002"
	uOtherG   = "00000000-0000-4000-8000-000000570003"
	uChild    = "00000000-0000-4000-8000-000000570004"
	uChild2   = "00000000-0000-4000-8000-000000570005"
	uViewer   = "00000000-0000-4000-8000-000000570006"
	libOpen   = "00000000-0000-4000-8000-00000057a000"
	libClosed = "00000000-0000-4000-8000-00000057b000"
)

type env struct {
	t     *testing.T
	db    *sql.DB
	dsn   string
	store *parental.Store
}

func setup(t *testing.T) *env {
	t.Helper()
	db, dsn := clustertest.ScratchDB(t)
	e := &env{t: t, db: db, dsn: dsn, store: parental.NewStore(db)}
	e.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES
		($1,'p-admin','x',true), ($2,'p-guardian','x',false), ($3,'p-other-guardian','x',false),
		($4,'p-child','x',false), ($5,'p-child-two','x',false), ($6,'p-viewer','x',false)`,
		uAdmin, uGuardian, uOtherG, uChild, uChild2, uViewer)
	e.exec(`INSERT INTO libraries (id, name, roots) VALUES ($1,'Secret Kids Films','{/media/p-open}'), ($2,'Grown Ups','{/media/p-closed}')`,
		libOpen, libClosed)
	e.exec(`INSERT INTO library_acl (user_id, library_id) VALUES ($1,$2)`, uChild, libOpen)
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatalf("exec %q: %v", q, err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// restrict makes id restricted at 9+ and returns the policy.
func (e *env) restrict(id string) *parental.Policy {
	e.t.Helper()
	p, err := e.store.SetPolicy(context.Background(), uAdmin, id, parental.PolicyInput{MaxTier: parental.Tier9, Unrated: parental.UnratedHide})
	if err != nil {
		e.t.Fatalf("restrict %s: %v", id, err)
	}
	return p
}

// T01: a policy on an admin, admin on a restricted account, and a restricted
// guardian are each refused, by the store and by the database under it.
func TestRestrictedIsNeverAdminOrGuardian(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	in := parental.PolicyInput{MaxTier: parental.Tier9, Unrated: parental.UnratedHide}

	if _, err := e.store.SetPolicy(ctx, uAdmin, uAdmin, in); !errors.Is(err, parental.ErrRestrictedAdmin) {
		t.Fatalf("policy on an admin: %v, want ErrRestrictedAdmin", err)
	}
	if n := e.count(`SELECT count(*) FROM parental_policies`); n != 0 {
		t.Fatalf("a refused policy was stored (%d rows)", n)
	}

	e.restrict(uChild)
	_, err := e.db.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, uChild)
	if !parental.IsRestrictedAdmin(err) {
		t.Fatalf("admin on a restricted account: %v, want the restricted-admin refusal", err)
	}
	if n := e.count(`SELECT count(*) FROM users WHERE id = $1 AND is_admin`, uChild); n != 0 {
		t.Fatal("a restricted account became an admin")
	}

	// a restricted account cannot be a guardian
	e.restrict(uChild2)
	if err := e.store.AddGuardian(ctx, uAdmin, uChild, uChild2); !errors.Is(err, parental.ErrRestrictedGuardian) {
		t.Fatalf("restricted guardian: %v, want ErrRestrictedGuardian", err)
	}
	// a guardian cannot be made restricted
	if err := e.store.AddGuardian(ctx, uAdmin, uChild, uGuardian); err != nil {
		t.Fatalf("add guardian: %v", err)
	}
	if _, err := e.store.SetPolicy(ctx, uAdmin, uGuardian, in); !errors.Is(err, parental.ErrRestrictedGuardian) {
		t.Fatalf("policy on a guardian: %v, want ErrRestrictedGuardian", err)
	}
	// nor can anyone look after themselves, or a user who is not restricted
	if err := e.store.AddGuardian(ctx, uAdmin, uChild, uChild); !errors.Is(err, parental.ErrSelfGuardian) && !errors.Is(err, parental.ErrRestrictedGuardian) {
		t.Fatalf("self guardian: %v", err)
	}
	if err := e.store.AddGuardian(ctx, uAdmin, uViewer, uGuardian); !errors.Is(err, parental.ErrNotRestricted) {
		t.Fatalf("guardian of an unrestricted user: %v, want ErrNotRestricted", err)
	}
	// once lifted, the account can be an admin again
	if err := e.store.Lift(ctx, uAdmin, uChild); err != nil {
		t.Fatalf("lift: %v", err)
	}
	e.exec(`UPDATE users SET is_admin = true WHERE id = $1`, uChild)
	if n := e.count(`SELECT count(*) FROM parental_guardians WHERE child_id = $1`, uChild); n != 0 {
		t.Fatal("lifting left guardians behind")
	}
}

func TestPolicyVersions(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	p := e.restrict(uChild)
	if p.Version != 1 || p.MaxTier != parental.Tier9 || p.UpdatedBy != uAdmin {
		t.Fatalf("new policy = %+v", p)
	}
	// a second "make restricted" (version 0) is stale, not an overwrite
	if _, err := e.store.SetPolicy(ctx, uAdmin, uChild, parental.PolicyInput{MaxTier: parental.Tier18, Unrated: parental.UnratedHide}); !errors.Is(err, parental.ErrStale) {
		t.Fatalf("version 0 on an existing policy: %v, want ErrStale", err)
	}
	p2, err := e.store.SetPolicy(ctx, uGuardian, uChild, parental.PolicyInput{MaxTier: parental.Tier13, Unrated: "tier_9", AllowOnly: true, Version: 1})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if p2.Version != 2 || p2.MaxTier != parental.Tier13 || p2.Unrated != "tier_9" || !p2.AllowOnly || p2.UpdatedBy != uGuardian {
		t.Fatalf("updated policy = %+v", p2)
	}
	if _, err := e.store.SetPolicy(ctx, uAdmin, uChild, parental.PolicyInput{MaxTier: parental.Tier9, Unrated: parental.UnratedHide, Version: 1}); !errors.Is(err, parental.ErrStale) {
		t.Fatalf("old version: %v, want ErrStale", err)
	}
	if _, err := e.store.SetPolicy(ctx, uAdmin, uChild, parental.PolicyInput{MaxTier: 12, Unrated: parental.UnratedHide, Version: 2}); !errors.Is(err, parental.ErrInvalid) {
		t.Fatalf("tier 12: %v, want ErrInvalid", err)
	}
	if _, err := e.store.SetPolicy(ctx, uAdmin, "00000000-0000-4000-8000-00000057ffff", parental.PolicyInput{MaxTier: parental.Tier9, Unrated: parental.UnratedHide}); !errors.Is(err, parental.ErrUserNotFound) {
		t.Fatalf("unknown user: %v, want ErrUserNotFound", err)
	}
	if _, err := e.store.Policy(ctx, uViewer); !errors.Is(err, parental.ErrNotRestricted) {
		t.Fatalf("policy of an unrestricted user: %v, want ErrNotRestricted", err)
	}
	if err := e.store.Lift(ctx, uAdmin, uViewer); !errors.Is(err, parental.ErrNotRestricted) {
		t.Fatalf("lift an unrestricted user: %v, want ErrNotRestricted", err)
	}
}

func TestGuardiansChildrenAndAccess(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.restrict(uChild)
	e.restrict(uChild2)
	if err := e.store.AddGuardian(ctx, uAdmin, uChild, uGuardian); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AddGuardian(ctx, uAdmin, uChild, uGuardian); err != nil {
		t.Fatalf("naming a guardian twice: %v", err)
	}
	if err := e.store.AddGuardian(ctx, uAdmin, uChild2, uOtherG); err != nil {
		t.Fatal(err)
	}
	gs, err := e.store.Guardians(ctx, uChild)
	if err != nil || len(gs) != 1 || gs[0].ID != uGuardian || gs[0].Username != "p-guardian" {
		t.Fatalf("guardians = %+v, %v", gs, err)
	}
	mine, err := e.store.Children(ctx, uGuardian, false)
	if err != nil || len(mine) != 1 || mine[0].ID != uChild {
		t.Fatalf("guardian's children = %+v, %v", mine, err)
	}
	all, err := e.store.Children(ctx, uAdmin, true)
	if err != nil || len(all) != 2 {
		t.Fatalf("admin's children = %+v, %v", all, err)
	}
	none, err := e.store.Children(ctx, uViewer, false)
	if err != nil || len(none) != 0 {
		t.Fatalf("viewer's children = %+v, %v", none, err)
	}
	access := func(p *principal.Principal, child string) parental.Access {
		t.Helper()
		a, err := e.store.AccessTo(ctx, p, child)
		if err != nil {
			t.Fatalf("AccessTo: %v", err)
		}
		return a
	}
	if a := access(&principal.Principal{UserID: uAdmin, IsAdmin: true}, uChild); a != parental.AccessAdmin {
		t.Fatalf("admin access = %v", a)
	}
	if a := access(&principal.Principal{UserID: uGuardian}, uChild); a != parental.AccessGuardian {
		t.Fatalf("guardian access = %v", a)
	}
	for who, p := range map[string]*principal.Principal{
		"other guardian": {UserID: uOtherG}, "viewer": {UserID: uViewer}, "the child": {UserID: uChild},
		// an admin signed in through a play-only cloud link is not an admin there
		"play-scoped admin": {UserID: uAdmin, IsAdmin: false, CloudScope: principal.CloudScopePlay},
	} {
		if a := access(p, uChild); a != parental.AccessNone {
			t.Errorf("%s access = %v, want none", who, a)
		}
	}
	if err := e.store.RemoveGuardian(ctx, uAdmin, uChild, uGuardian); err != nil {
		t.Fatal(err)
	}
	if err := e.store.RemoveGuardian(ctx, uAdmin, uChild, uGuardian); !errors.Is(err, parental.ErrGuardianNotFound) {
		t.Fatalf("removing a guardian twice: %v, want ErrGuardianNotFound", err)
	}
	if a := access(&principal.Principal{UserID: uGuardian}, uChild); a != parental.AccessNone {
		t.Fatal("a removed guardian still has access")
	}
	// deleting a guardian's account leaves the child restricted, looked after by admins
	if err := e.store.AddGuardian(ctx, uAdmin, uChild, uGuardian); err != nil {
		t.Fatal(err)
	}
	e.exec(`DELETE FROM users WHERE id = $1`, uGuardian)
	if ok, _ := e.store.IsRestricted(ctx, uChild); !ok {
		t.Fatal("deleting the guardian lifted the child's restriction")
	}
}

// T04 (store side): rules, and an allow on a library the child cannot open.
func TestRules(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.restrict(uChild)
	if _, err := e.store.AddRule(ctx, uAdmin, uChild, parental.RuleInput{Effect: parental.EffectAllow, SubjectKind: parental.SubjectLibrary, SubjectID: libClosed}); !errors.Is(err, parental.ErrNoLibraryAccess) {
		t.Fatalf("allow on a library without access: %v, want ErrNoLibraryAccess", err)
	}
	if n := e.count(`SELECT count(*) FROM parental_rules`); n != 0 {
		t.Fatal("a refused rule was stored")
	}
	// a block needs no access: it only takes away
	if _, err := e.store.AddRule(ctx, uAdmin, uChild, parental.RuleInput{Effect: parental.EffectBlock, SubjectKind: parental.SubjectLibrary, SubjectID: libClosed}); err != nil {
		t.Fatalf("block on a library without access: %v", err)
	}
	r, err := e.store.AddRule(ctx, uGuardian, uChild, parental.RuleInput{Effect: parental.EffectAllow, SubjectKind: parental.SubjectLibrary, SubjectID: libOpen})
	if err != nil {
		t.Fatalf("allow on an open library: %v", err)
	}
	if r.ID == "" || r.UserID != uChild || r.CreatedBy != uGuardian {
		t.Fatalf("rule = %+v", r)
	}
	// the same subject and effect again replaces (here: gains an expiry), not doubles
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	r2, err := e.store.AddRule(ctx, uAdmin, uChild, parental.RuleInput{Effect: parental.EffectAllow, SubjectKind: parental.SubjectLibrary, SubjectID: libOpen, ExpiresAt: &exp})
	if err != nil || r2.ID != r.ID || r2.ExpiresAt == nil || !r2.ExpiresAt.Equal(exp) {
		t.Fatalf("re-adding a rule = %+v, %v", r2, err)
	}
	if _, err := e.store.AddRule(ctx, uAdmin, uChild, parental.RuleInput{Effect: "maybe", SubjectKind: parental.SubjectVideo, SubjectID: "x"}); !errors.Is(err, parental.ErrInvalid) {
		t.Fatalf("bad effect: %v, want ErrInvalid", err)
	}
	if _, err := e.store.AddRule(ctx, uAdmin, uViewer, parental.RuleInput{Effect: parental.EffectBlock, SubjectKind: parental.SubjectPlugin, SubjectID: "tv.maktaba.x"}); !errors.Is(err, parental.ErrNotRestricted) {
		t.Fatalf("rule on an unrestricted user: %v, want ErrNotRestricted", err)
	}
	rules, err := e.store.Rules(ctx, uChild)
	if err != nil || len(rules) != 2 {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	// every rule change moved the version, so a reader with the old one is stale
	if p, _ := e.store.Policy(ctx, uChild); p.Version != 4 {
		t.Fatalf("version after three rule changes = %d, want 4", p.Version)
	}
	eff, err := e.store.Effective(ctx, uChild)
	if err != nil || !eff.Restricted || len(eff.Rules) != 2 ||
		!eff.Blocked(parental.Subject{Kind: parental.SubjectLibrary, ID: libClosed}, time.Now()) ||
		!eff.AllowedBy(parental.Subject{Kind: parental.SubjectLibrary, ID: libOpen}, time.Now()) {
		t.Fatalf("effective = %+v, %v", eff, err)
	}
	if un, err := e.store.Effective(ctx, uViewer); err != nil || un.Restricted {
		t.Fatalf("effective of an unrestricted user = %+v, %v", un, err)
	}
	// another child's rule id does not remove this child's rule
	e.restrict(uChild2)
	if err := e.store.RemoveRule(ctx, uAdmin, uChild2, r.ID); !errors.Is(err, parental.ErrRuleNotFound) {
		t.Fatalf("removing through another child: %v, want ErrRuleNotFound", err)
	}
	if err := e.store.RemoveRule(ctx, uAdmin, uChild, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.RemoveRule(ctx, uAdmin, uChild, r.ID); !errors.Is(err, parental.ErrRuleNotFound) {
		t.Fatalf("removing twice: %v, want ErrRuleNotFound", err)
	}
	// lifting takes the rules with it
	if err := e.store.Lift(ctx, uAdmin, uChild); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM parental_rules WHERE user_id = $1`, uChild); n != 0 {
		t.Fatal("lifting left rules behind")
	}
}

// T07: each change writes exactly one audit row, with ids and tiers and no title
// or name.
func TestAuditRows(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rows := func() int { return e.count(`SELECT count(*) FROM audit_log WHERE action LIKE 'parental.%'`) }
	step := func(name, action string, fn func() error) {
		t.Helper()
		before := rows()
		if err := fn(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := rows() - before; got != 1 {
			t.Fatalf("%s wrote %d audit rows, want 1", name, got)
		}
		var act, actor, target, kind, cat string
		if err := e.db.QueryRow(`SELECT action, actor_user_id::text, target_id::text, target_kind, category::text
			FROM audit_log WHERE action LIKE 'parental.%' ORDER BY id DESC LIMIT 1`).Scan(&act, &actor, &target, &kind, &cat); err != nil {
			t.Fatalf("%s: read audit row: %v", name, err)
		}
		if act != action || actor != uAdmin || target != uChild || kind != "user" {
			t.Fatalf("%s: audit row = %s by %s on %s (%s, %s)", name, act, actor, target, kind, cat)
		}
	}
	var ruleID string
	step("make restricted", parental.AuditPolicySet, func() error {
		_, err := e.store.SetPolicy(ctx, uAdmin, uChild, parental.PolicyInput{MaxTier: parental.Tier9, Unrated: parental.UnratedHide})
		return err
	})
	step("change the limit", parental.AuditPolicySet, func() error {
		_, err := e.store.SetPolicy(ctx, uAdmin, uChild, parental.PolicyInput{MaxTier: parental.Tier13, Unrated: parental.UnratedHide, Version: 1})
		return err
	})
	step("name a guardian", parental.AuditGuardianAdded, func() error { return e.store.AddGuardian(ctx, uAdmin, uChild, uGuardian) })
	step("add a rule", parental.AuditRuleAdded, func() error {
		r, err := e.store.AddRule(ctx, uAdmin, uChild, parental.RuleInput{Effect: parental.EffectAllow, SubjectKind: parental.SubjectLibrary, SubjectID: libOpen})
		if r != nil {
			ruleID = r.ID
		}
		return err
	})
	step("remove a rule", parental.AuditRuleRemoved, func() error { return e.store.RemoveRule(ctx, uAdmin, uChild, ruleID) })
	step("remove a guardian", parental.AuditGuardianRemoved, func() error { return e.store.RemoveGuardian(ctx, uAdmin, uChild, uGuardian) })
	step("lift", parental.AuditPolicyLifted, func() error { return e.store.Lift(ctx, uAdmin, uChild) })

	// a refused change writes nothing
	before := rows()
	_, _ = e.store.SetPolicy(ctx, uAdmin, uAdmin, parental.PolicyInput{MaxTier: parental.Tier9, Unrated: parental.UnratedHide})
	if rows() != before {
		t.Fatal("a refused change wrote an audit row")
	}

	// before and after are there; no name or title is
	payloads, err := e.db.Query(`SELECT action, payload::text FROM audit_log WHERE action LIKE 'parental.%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer payloads.Close()
	for payloads.Next() {
		var action, raw string
		if err := payloads.Scan(&action, &raw); err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("%s payload: %v", action, err)
		}
		if _, ok := body["before"]; !ok {
			t.Errorf("%s payload has no before: %s", action, raw)
		}
		if _, ok := body["after"]; !ok {
			t.Errorf("%s payload has no after: %s", action, raw)
		}
		for _, leak := range []string{"Secret Kids Films", "p-child", "p-guardian", "p-admin", "title", "username", "name"} {
			if strings.Contains(raw, leak) {
				t.Errorf("%s payload holds %q: %s", action, leak, raw)
			}
		}
	}
	if err := payloads.Err(); err != nil {
		t.Fatal(err)
	}
	var first string
	if err := e.db.QueryRow(`SELECT payload::text FROM audit_log WHERE action = $1 ORDER BY id LIMIT 1 OFFSET 1`, parental.AuditPolicySet).Scan(&first); err != nil {
		t.Fatal(err)
	}
	var change struct {
		Before, After struct {
			MaxTier int `json:"max_tier"`
		}
	}
	if err := json.Unmarshal([]byte(first), &change); err != nil || change.Before.MaxTier != 9 || change.After.MaxTier != 13 {
		t.Fatalf("limit change payload = %s (%v), want before 9 after 13", first, err)
	}
}

// T05: a tightened policy holds on the next read, with no restart: this process
// drops its copy at once, and another process (a second cache on the same
// database) hears the notification.
func TestCacheSeesAChangeOnTheNextRead(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.restrict(uChild) // 9+
	e.exec(`UPDATE parental_policies SET max_tier = 16 WHERE user_id = $1`, uChild)

	cache := parental.NewCache(e.store)
	done := make(chan struct{})
	go func() { defer close(done); cache.Listen(ctx, e.dsn, nil, nil) }()
	deadline := time.Now().Add(10 * time.Second)
	for !cache.Listening() {
		if time.Now().After(deadline) {
			t.Fatal("the cache never started listening")
		}
		time.Sleep(10 * time.Millisecond)
	}
	read := func() *parental.Effective {
		t.Helper()
		eff, err := cache.For(ctx, &principal.Principal{UserID: uChild})
		if err != nil {
			t.Fatalf("cache read: %v", err)
		}
		return eff
	}
	sixteen := 16
	if eff := read(); !eff.Restricted || !eff.Permits(parental.Item{Tier: &sixteen}, time.Now()) {
		t.Fatalf("before tightening: %+v", eff)
	}
	// "another process" tightens the limit: a plain write, with no call into this cache
	other := parental.NewStore(e.db)
	p, _ := other.Policy(ctx, uChild)
	if _, err := other.SetPolicy(ctx, uAdmin, uChild, parental.PolicyInput{MaxTier: parental.Tier9, Unrated: parental.UnratedHide, Version: p.Version}); err != nil {
		t.Fatalf("tighten: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		eff := read()
		if !eff.Permits(parental.Item{Tier: &sixteen}, time.Now()) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the cache still shows a 16+ title five seconds after the limit became 9+")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// a rule added elsewhere arrives the same way
	if _, err := other.AddRule(ctx, uAdmin, uChild, parental.RuleInput{Effect: parental.EffectBlock, SubjectKind: parental.SubjectVideo, SubjectID: "v-1"}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for !read().Blocked(parental.Subject{Kind: parental.SubjectVideo, ID: "v-1"}, time.Now()) {
		if time.Now().After(deadline) {
			t.Fatal("the cache never saw the new block rule")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// this process's own change is dropped at once, with no wait
	e.exec(`ALTER TABLE parental_policies DISABLE TRIGGER USER`)
	e.exec(`UPDATE parental_policies SET max_tier = -1 WHERE user_id = $1`, uChild)
	e.exec(`ALTER TABLE parental_policies ENABLE TRIGGER USER`)
	cache.Invalidate(uChild)
	zero := 0
	if read().Permits(parental.Item{Tier: &zero}, time.Now()) {
		t.Fatal("Invalidate did not drop the cached policy")
	}
	// an unrestricted account and a caller with no user id are never limited
	if eff, err := cache.For(ctx, &principal.Principal{UserID: uViewer}); err != nil || eff.Restricted {
		t.Fatalf("viewer: %+v, %v", eff, err)
	}
	if eff, err := cache.For(ctx, &principal.Principal{UserID: "admin-token", IsAdmin: true}); err != nil || eff.Restricted {
		t.Fatalf("admin token: %+v, %v", eff, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Listen did not stop when its context ended")
	}
	if cache.Listening() {
		t.Fatal("the cache still says it listens after Listen returned")
	}
}

// Without a listener nothing is cached: every read is the database's.
func TestCacheWithoutAListenerReadsEveryTime(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.restrict(uChild)
	cache := parental.NewCache(e.store)
	if eff, err := cache.Effective(ctx, uChild); err != nil || eff.MaxTier != parental.Tier9 {
		t.Fatalf("first read: %+v, %v", eff, err)
	}
	e.exec(`UPDATE parental_policies SET max_tier = 0 WHERE user_id = $1`, uChild)
	if eff, err := cache.Effective(ctx, uChild); err != nil || eff.MaxTier != parental.TierAll {
		t.Fatalf("second read: %+v, %v", eff, err)
	}
}

// T06 (store side): who may approve a device as whom.
func TestApproveAs(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.restrict(uChild)
	e.restrict(uChild2)
	if err := e.store.AddGuardian(ctx, uAdmin, uChild, uGuardian); err != nil {
		t.Fatal(err)
	}
	admin := &principal.Principal{UserID: uAdmin, IsAdmin: true}
	guardian := &principal.Principal{UserID: uGuardian}
	child := &principal.Principal{UserID: uChild}
	viewer := &principal.Principal{UserID: uViewer}
	cases := []struct {
		name   string
		p      *principal.Principal
		as     string
		wantID string
		want   error
	}{
		{"no one named", guardian, "", uGuardian, nil},
		{"oneself", viewer, uViewer, uViewer, nil},
		{"a child approves for itself", child, "", uChild, nil},
		{"a guardian as their child", guardian, uChild, uChild, nil},
		{"an admin as any child", admin, uChild2, uChild2, nil},
		{"a guardian as another's child", guardian, uChild2, "", parental.ErrNotRestricted},
		{"a guardian as an unrestricted user", guardian, uViewer, "", parental.ErrNotRestricted},
		{"an admin as an unrestricted user", admin, uViewer, "", parental.ErrNotRestricted},
		{"a viewer as a child", viewer, uChild, "", parental.ErrNotRestricted},
		{"a child as its guardian", child, uGuardian, "", parental.ErrApproveForbidden},
		{"a child as another child", child, uChild2, "", parental.ErrApproveForbidden},
		{"an id that is no id", guardian, "nobody", "", parental.ErrNotRestricted},
	}
	for _, c := range cases {
		id, err := e.store.ApproveAs(ctx, c.p, c.as)
		if c.want == nil {
			if err != nil || id != c.wantID {
				t.Errorf("%s: (%q, %v), want %q", c.name, id, err, c.wantID)
			}
			continue
		}
		if !errors.Is(err, c.want) && !(errors.Is(c.want, parental.ErrNotRestricted) && errors.Is(err, parental.ErrUserNotFound)) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if id != "" {
			t.Errorf("%s: a refusal still named %q", c.name, id)
		}
	}
}
