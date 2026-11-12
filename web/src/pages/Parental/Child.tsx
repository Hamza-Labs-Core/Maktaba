// Epic 57 family settings on the web: one account (Family > the account).
//
//   GET|PUT|DELETE /api/users/{id}/parental            the limit; lifting is the admin's
//   POST   /api/users/{id}/parental/guardians          admin
//   DELETE /api/users/{id}/parental/guardians/{gid}    admin
//   POST   /api/users/{id}/parental/signals/{family}/clear
//
// The home answers 404 for an account that is not restricted, and for one the
// caller does not look after. An admin then gets the form that makes the account
// restricted (the first PUT, with no version); anyone else is told it is not theirs.
// Every role is the home's: the page shows the admin's controls when the home's
// answer says `access: admin`, and shows the home's refusal when it refuses.
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { useOptionalAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { useMyParental } from "../../lib/parental";
import { tierKey } from "../../lib/parentalApi";
import {
  POLICY_LIMITS,
  UNRATED,
  addGuardian,
  clearSignal,
  fetchAccounts,
  fetchPolicy,
  liftRestriction,
  removeGuardian,
  savePolicy,
  type ChildPolicy,
  type DeviceSignal,
  type Person,
  type Unrated,
} from "../../lib/familyApi";
import { usePageData } from "../../lib/usePageData";
import { LoadError } from "../../components/LoadError";
import { BackButton } from "../../components/BackButton";
import { TitleRow } from "../../help/HelpLink";
import { ChildRules } from "./Rules";
import { familyError, isStale } from "./words";
import "../../styles/parental.css";

export const childPath = (userId: string) => `/family/${encodeURIComponent(userId)}`;
export const timePath = (userId: string) => `${childPath(userId)}/time`;
export const activityPath = (userId: string) => `${childPath(userId)}/activity`;

interface Loaded {
  /** null: the home answered 404 */
  policy: ChildPolicy | null;
  /** the home's accounts (an admin only; empty for anyone else) */
  accounts: Person[];
}

export function ChildPage() {
  const { userId = "" } = useParams();
  const { t } = useI18n();
  const admin = useOptionalAuth()?.user?.is_admin === true;
  const parental = useMyParental();
  const { data, err, loading, reload, setData } = usePageData<Loaded>(
    userId ? `parental:child:${userId}` : null,
    async () => {
      const policy = await fetchPolicy(userId);
      // names to pick from, and this account's own: the admin's alone
      const accounts = admin ? await fetchAccounts().catch(() => []) : [];
      return { policy, accounts };
    },
    [userId, admin]
  );

  if (loading) return <p className="mkt-loading">{t("common.loading")}</p>;
  if (!data) {
    return (
      <section className="mkt-page">
        <BackButton fallback="/family" />
        <LoadError error={err} onRetry={reload} backTo="/family" />
      </section>
    );
  }
  const name =
    parental.children.find((c) => c.id === userId)?.username ??
    data.accounts.find((a) => a.id === userId)?.username ??
    "";
  const setPolicy = (policy: ChildPolicy | null) =>
    setData((prev) => (prev ? { ...prev, policy } : prev));

  if (!data.policy) {
    return (
      <section className="mkt-page mkt-parental-child">
        <BackButton fallback="/family" />
        {admin ? (
          <>
            <TitleRow>
              <h1>
                {t("family.makeRestricted")}
                {name && (
                  <>
                    : <bdi>{name}</bdi>
                  </>
                )}
              </h1>
            </TitleRow>
            <p className="mkt-parental-note">{t("family.makeRestricted.intro")}</p>
            <PolicyForm
              userId={userId}
              policy={null}
              onSaved={(p) => {
                setPolicy(p);
                parental.refresh();
              }}
              onStale={reload}
            />
          </>
        ) : (
          <ErrorState
            kind="not_found"
            title={t("family.error.notFound")}
            action={
              <Link className="mkt-btn mkt-btn--secondary" to="/family">
                {t("family.title")}
              </Link>
            }
          />
        )}
      </section>
    );
  }

  const policy = data.policy;
  const isAdmin = policy.access === "admin";
  return (
    <section className="mkt-page mkt-parental-child">
      <BackButton fallback="/family" />
      <TitleRow>
        <h1>
          <bdi>{name || t("family.child.unnamed")}</bdi>
        </h1>
      </TitleRow>
      <ul className="mkt-more__list" data-testid="family-child-links">
        <li>
          <Link className="mkt-more__link" to={timePath(userId)}>
            {t("parental.time.rules")}
          </Link>
        </li>
        <li>
          <Link className="mkt-more__link" to={activityPath(userId)}>
            {t("family.child.activity")}
          </Link>
        </li>
      </ul>

      <h2>{t("family.child.limit")}</h2>
      <PolicyForm
        userId={userId}
        policy={policy}
        onSaved={(p) => {
          setPolicy(p);
          parental.refresh();
        }}
        onStale={reload}
      />

      <ChildRules userId={userId} />

      <Devices userId={userId} signals={policy.signals} onChanged={reload} />

      {isAdmin && (
        <Guardians
          userId={userId}
          guardians={policy.guardians}
          accounts={data.accounts}
          onChanged={(guardians) => setPolicy({ ...policy, guardians })}
        />
      )}
      {!isAdmin && policy.guardians.length > 0 && (
        <>
          <h2>{t("family.child.guardians")}</h2>
          <ul className="mkt-parental-list">
            {policy.guardians.map((g) => (
              <li key={g.id}>
                <bdi>{g.username || g.id}</bdi>
              </li>
            ))}
          </ul>
        </>
      )}

      {isAdmin && <Lift userId={userId} name={name} />}
    </section>
  );
}

// ---- the limit ----

function PolicyForm({
  userId,
  policy,
  onSaved,
  onStale,
}: {
  userId: string;
  /** null: the account is not restricted yet */
  policy: ChildPolicy | null;
  onSaved: (next: ChildPolicy) => void;
  onStale: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [maxTier, setMaxTier] = useState<number>(policy?.maxTier ?? 9);
  const [unrated, setUnrated] = useState<Unrated>(policy?.unrated ?? "hide");
  const [allowOnly, setAllowOnly] = useState(policy?.allowOnly ?? false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!policy) return;
    setMaxTier(policy.maxTier);
    setUnrated(policy.unrated);
    setAllowOnly(policy.allowOnly);
  }, [policy]);

  const dirty =
    !policy ||
    policy.maxTier !== maxTier ||
    policy.unrated !== unrated ||
    policy.allowOnly !== allowOnly;

  async function save() {
    setBusy(true);
    setError(null);
    try {
      const next = await savePolicy(userId, {
        maxTier,
        unrated,
        allowOnly,
        version: policy?.version ?? 0,
      });
      onSaved(next);
      toast.show({
        tone: "success",
        message: t(policy ? "family.child.saved" : "family.makeRestricted.done"),
      });
    } catch (e) {
      if (isStale(e)) {
        // someone else changed it: show theirs
        toast.show({ tone: "warn", message: t("family.conflict") });
        onStale();
      } else {
        setError(familyError(e, t));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mkt-parental-policy" data-testid="family-policy">
      <Select
        label={t("family.child.limit")}
        description={t("family.child.limit.desc")}
        value={String(maxTier)}
        onChange={(e) => setMaxTier(Number(e.target.value))}
      >
        {POLICY_LIMITS.map((tier) => (
          <option key={tier} value={tier}>
            {t(tierKey(tier))}
          </option>
        ))}
      </Select>
      <Select
        label={t("family.child.unrated")}
        description={t("family.child.unrated.desc")}
        value={unrated}
        onChange={(e) => setUnrated(e.target.value as Unrated)}
      >
        {UNRATED.map((u) => (
          <option key={u} value={u}>
            {u === "hide"
              ? t("family.child.unrated.hide")
              : t("family.child.unrated.as", { tier: t(tierKey(Number(u.slice(5)))) })}
          </option>
        ))}
      </Select>
      <Checkbox
        checked={allowOnly}
        onChange={(e) => setAllowOnly(e.target.checked)}
        label={t("family.child.allowOnly")}
      />
      <p className="mkt-parental-note">{t("family.child.allowOnly.desc")}</p>
      {error && (
        <p className="mkt-alert" role="alert">
          {error}
        </p>
      )}
      <div className="mkt-parental-actions">
        <Button onClick={() => void save()} disabled={!dirty || busy} loading={busy}>
          {t(policy ? "common.save" : "family.makeRestricted")}
        </Button>
      </div>
    </div>
  );
}

// ---- guardians (admin) ----

function Guardians({
  userId,
  guardians,
  accounts,
  onChanged,
}: {
  userId: string;
  guardians: Person[];
  accounts: Person[];
  onChanged: (next: Person[]) => void;
}) {
  const { t, locale } = useI18n();
  const [pick, setPick] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const free = accounts
    .filter((a) => a.id !== userId && !guardians.some((g) => g.id === a.id))
    .sort((a, b) => a.username.localeCompare(b.username, locale));

  async function run(work: () => Promise<Person[]>) {
    setBusy(true);
    setError(null);
    try {
      onChanged(await work());
      setPick("");
    } catch (e) {
      setError(familyError(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-labelledby="family-guardians" data-testid="family-guardians">
      <h2 id="family-guardians">{t("family.child.guardians")}</h2>
      <p className="mkt-parental-note">{t("family.guardians.intro")}</p>
      {guardians.length === 0 ? (
        <p className="mkt-muted">{t("family.guardians.none")}</p>
      ) : (
        <ul className="mkt-parental-list">
          {guardians.map((g) => (
            <li key={g.id}>
              <bdi>{g.username || g.id}</bdi>
              <Button
                variant="secondary"
                size="sm"
                disabled={busy}
                aria-label={`${t("family.remove")} ${g.username || g.id}`}
                onClick={() =>
                  void run(async () => {
                    await removeGuardian(userId, g.id);
                    return guardians.filter((x) => x.id !== g.id);
                  })
                }
              >
                {t("family.remove")}
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="mkt-parental-row">
        <Select
          label={t("family.guardians.add")}
          value={pick}
          onChange={(e) => setPick(e.target.value)}
        >
          <option value="">{t("family.accounts.choose")}</option>
          {free.map((a) => (
            <option key={a.id} value={a.id}>
              {a.username || a.id}
            </option>
          ))}
        </Select>
        <Button
          variant="secondary"
          disabled={!pick || busy}
          onClick={() => void run(() => addGuardian(userId, pick))}
        >
          {t("family.add")}
        </Button>
      </div>
      {error && (
        <p className="mkt-alert" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}

// ---- devices ----

/** What a device said about its user's age, in words; "" when it said nothing. */
function ageWords(s: DeviceSignal, t: ReturnType<typeof useI18n>["t"], n: (x: number) => string) {
  if (s.lower !== null && s.upper !== null)
    return t("family.device.ageRange", { from: n(s.lower), to: n(s.upper) });
  if (s.lower !== null) return t("family.device.ageFrom", { from: n(s.lower) });
  if (s.upper !== null) return t("family.device.ageTo", { to: n(s.upper) });
  return "";
}

function Devices({
  userId,
  signals,
  onChanged,
}: {
  userId: string;
  signals: DeviceSignal[];
  onChanged: () => void;
}) {
  const { t, n, formatDate } = useI18n();
  const toast = useToast();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const limitWords = (tier: number | null) =>
    tier === null ? t("family.device.noLimit") : t(tierKey(tier));

  async function clear(s: DeviceSignal) {
    setBusy(s.family);
    setError(null);
    try {
      const got = await clearSignal(userId, s.family);
      toast.show({
        tone: "success",
        message: got.removed
          ? t("family.device.removed")
          : t("family.device.approved", { tier: limitWords(got.tierLimit) }),
      });
      onChanged();
    } catch (e) {
      setError(familyError(e, t));
    } finally {
      setBusy(null);
    }
  }

  return (
    <section aria-labelledby="family-devices" data-testid="family-devices">
      <h2 id="family-devices">{t("family.child.devices")}</h2>
      <p className="mkt-parental-note">{t("family.device.intro")}</p>
      {signals.length === 0 ? (
        <p className="mkt-muted">{t("family.device.none")}</p>
      ) : (
        <ul className="mkt-parental-list">
          {signals.map((s, i) => {
            const age = ageWords(s, t, n);
            return (
              <li key={s.family} data-testid="family-device">
                <div className="mkt-parental-list__main">
                  <strong>{t("family.device.name", { number: n(i + 1) })}</strong>
                  <span>
                    {t("family.device.limit", { tier: limitWords(s.tierLimit) })}
                    {age && <> · {age}</>}
                  </span>
                  <span className="mkt-muted">
                    {t("family.device.seen", { when: formatDate(s.observedAt) })}
                  </span>
                  {s.pending && (
                    <Badge tone="warn">
                      {t("family.device.pending", { tier: limitWords(s.pending.tierLimit) })}
                    </Badge>
                  )}
                </div>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={busy !== null}
                  loading={busy === s.family}
                  onClick={() => void clear(s)}
                >
                  {t(s.pending ? "family.device.approve" : "family.device.clear")}
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      {error && (
        <p className="mkt-alert" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}

// ---- lifting (admin) ----

function Lift({ userId, name }: { userId: string; name: string }) {
  const { t } = useI18n();
  const toast = useToast();
  const navigate = useNavigate();
  const parental = useMyParental();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function lift() {
    setBusy(true);
    setError(null);
    try {
      await liftRestriction(userId);
      toast.show({ tone: "success", message: t("family.child.lift.done") });
      parental.refresh();
      navigate("/family");
    } catch (e) {
      setError(familyError(e, t));
      setBusy(false);
    }
  }

  return (
    <section aria-labelledby="family-lift">
      <h2 id="family-lift">{t("family.child.lift")}</h2>
      <p className="mkt-parental-note">{t("family.child.lift.desc")}</p>
      <Button variant="destructive" onClick={() => setOpen(true)}>
        {t("family.child.lift")}
      </Button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={t("family.child.lift")}
        dismissable={false}
        footer={
          <>
            <Button variant="secondary" onClick={() => setOpen(false)} disabled={busy}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="destructive"
              onClick={() => void lift()}
              loading={busy}
              disabled={busy}
            >
              {t("family.child.lift.confirmButton")}
            </Button>
          </>
        }
      >
        <p>{t("family.child.lift.confirm", { name: name || t("family.child.unnamed") })}</p>
        <p>{t("family.child.lift.removes")}</p>
        <ul data-testid="family-lift-removes">
          <li>{t("family.child.lift.removes.rules")}</li>
          <li>{t("family.child.lift.removes.requests")}</li>
          <li>{t("family.child.lift.removes.activity")}</li>
          <li>{t("family.child.lift.removes.time")}</li>
          <li>{t("family.child.lift.removes.guardians")}</li>
        </ul>
        {error && (
          <p className="mkt-alert" role="alert">
            {error}
          </p>
        )}
      </Modal>
    </section>
  );
}
