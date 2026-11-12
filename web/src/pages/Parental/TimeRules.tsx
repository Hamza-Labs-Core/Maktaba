// Epic 57 story 57.15: a restricted account's time rules on the web
// (Users > the user > Time), for an admin or that account's guardian.
//
//   GET|PUT /api/parental/users/{id}/time-rules   {rules: [...]} → {rules, time_zone}
//   GET     /api/parental/users/{id}/time-usage   the last seven home days, the grants
//   POST    /api/parental/users/{id}/time-grants  {minutes}: more time today
//
// A rule has days, watching hours (a slot), a daily limit and, optionally, one
// library; every rule that covers a moment must allow it. The home keeps the
// clock, in its own time zone, and decides every role: a viewer or the child gets
// 403 here, a guardian of another child 404, and the page says so.
import { useEffect, useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { dateTimeFormat } from "../../lib/intl";
import { fetchLibrarySummary } from "../../lib/browse";
import {
  ALL_DAYS,
  FIRST_RULE,
  MAX_GRANT_MINUTES,
  MAX_TIME_RULES,
  fetchTimeRules,
  fetchTimeUsage,
  grantTime,
  saveTimeRules,
  type TimeRule,
  type TimeRules as Rules,
  type TimeUsage,
} from "../../lib/parentalApi";
import { useMyParental } from "../../lib/parental";
import { usePageData } from "../../lib/usePageData";
import { LoadError } from "../../components/LoadError";
import { BackButton } from "../../components/BackButton";
import { TitleRow } from "../../help/HelpLink";
import { timeWords } from "../../components/parental/TimeState";
import "../../styles/parental.css";

/** The minutes one tap gives. */
export const GRANT_CHOICES = [15, 30, 60] as const;
/** The longest daily limit the editor takes: a whole day. */
const MAX_DAILY_MINUTES = 24 * 60;

/** Why a rule cannot be saved (a catalog key), or null. The home checks again. */
export function ruleProblem(r: TimeRule): string | null {
  if ((r.days & ALL_DAYS) === 0) return "parental.time.error.days";
  const slot = r.slotStart !== "" || r.slotEnd !== "";
  if (slot && (r.slotStart === "" || r.slotEnd === "" || r.slotStart === r.slotEnd))
    return "parental.time.error.slot";
  if (
    r.dailyMinutes !== null &&
    (!Number.isInteger(r.dailyMinutes) || r.dailyMinutes < 0 || r.dailyMinutes > MAX_DAILY_MINUTES)
  )
    return "parental.time.error.daily";
  if (!slot && r.dailyMinutes === null) return "parental.time.error.empty";
  return null;
}

/** The seven day names, Sunday first (bit 0), in the page's language. */
function useDayNames(): string[] {
  const { locale } = useI18n();
  return useMemo(() => {
    const fmt = dateTimeFormat(locale === "ar" ? "ar-EG" : "en", {
      weekday: "long",
      timeZone: "UTC",
    });
    // 2024-01-07 was a Sunday
    return Array.from({ length: 7 }, (_, i) => fmt.format(new Date(Date.UTC(2024, 0, 7 + i))));
  }, [locale]);
}

interface Loaded {
  rules: Rules;
  usage: TimeUsage | null;
  libraries: Array<{ id: string; name: string }>;
}

export function TimeRulesPage() {
  const { userId = "" } = useParams();
  const words = useI18n();
  const { t, n } = words;
  const toast = useToast();
  const { children } = useMyParental();
  const name = children.find((c) => c.id === userId)?.username ?? "";
  const dayNames = useDayNames();

  const { data, err, loading, reload, setData } = usePageData<Loaded>(
    userId ? `parental:time:${userId}` : null,
    async () => {
      // the rules decide whether the page opens at all; the rest only adds to it
      const rules = await fetchTimeRules(userId);
      const [usage, libraries] = await Promise.all([
        fetchTimeUsage(userId).catch(() => null),
        fetchLibrarySummary()
          .then((ls) => ls.map((l) => ({ id: l.id, name: l.name })))
          .catch(() => []),
      ]);
      return { rules, usage, libraries };
    },
    [userId]
  );

  const [draft, setDraft] = useState<TimeRule[] | null>(null);
  const [usage, setUsage] = useState<TimeUsage | null>(null);
  const [saving, setSaving] = useState(false);
  const [granting, setGranting] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  // The draft follows the saved rules when they change. A fresh read that says the
  // same (the quiet re-read on coming back to the page) keeps what is being edited.
  const savedRules = data ? JSON.stringify(data.rules.rules) : null;
  useEffect(() => {
    setDraft(savedRules === null ? null : (JSON.parse(savedRules) as TimeRule[]));
    setSaveError(null);
  }, [savedRules]);
  useEffect(() => {
    setUsage(data?.usage ?? null);
  }, [data]);

  if (loading) return <p className="mkt-loading">{t("common.loading")}</p>;
  if (!data || !draft) {
    return (
      <section className="mkt-page">
        <BackButton fallback="/family" />
        <LoadError
          error={err}
          onRetry={reload}
          backTo="/family"
          // the home's 404: not a restricted account, or not one this person looks after
          title={
            err instanceof ApiError && err.status === 404 ? t("parental.time.notFound") : undefined
          }
        />
      </section>
    );
  }

  const saved = data.rules.rules;
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);
  const problems = draft.map(ruleProblem);
  const invalid = problems.some((p) => p !== null);
  const change = (i: number, patch: Partial<TimeRule>) =>
    setDraft((d) => (d ? d.map((r, j) => (j === i ? { ...r, ...patch } : r)) : d));
  const libraryName = (id: string) => data.libraries.find((l) => l.id === id)?.name ?? id;

  async function save() {
    if (!draft || invalid) return;
    setSaving(true);
    setSaveError(null);
    try {
      const next = await saveTimeRules(userId, draft);
      // the page's kept answer is the saved one from here on
      setData((prev) => (prev ? { ...prev, rules: next } : prev));
      toast.show({ tone: "success", message: t("parental.time.saved.rules") });
    } catch (e) {
      // the home's refusal, in the app's words: who may change rules, or what is wrong
      setSaveError(
        e instanceof ApiError && e.status === 403
          ? t("parental.time.error.forbidden")
          : e instanceof ApiError && e.status === 422
            ? t("parental.time.error.invalid")
            : t("common.error")
      );
    } finally {
      setSaving(false);
    }
  }

  async function give(minutes: number) {
    setGranting(true);
    try {
      await grantTime(userId, minutes);
      toast.show({
        tone: "success",
        message: t("parental.time.granted", { time: timeWords(minutes * 60, words) }),
      });
      setUsage(await fetchTimeUsage(userId).catch(() => usage));
    } catch (e) {
      toast.show({
        tone: "error",
        message:
          e instanceof ApiError && e.status === 403
            ? t("parental.time.error.forbidden")
            : t("common.error"),
      });
    } finally {
      setGranting(false);
    }
  }

  const today = usage?.days.find((d) => d.day === usage.today)?.seconds ?? 0;
  const grantsToday = usage?.grants.filter((g) => g.day === usage.today) ?? [];
  const grantedToday = grantsToday.reduce((sum, g) => sum + g.minutes, 0);

  return (
    <section className="mkt-page mkt-parental-timepage">
      <BackButton fallback="/family" />
      <TitleRow>
        <h1>
          {name ? (
            <>
              {t("parental.time.rules")}: <bdi>{name}</bdi>
            </>
          ) : (
            t("parental.time.rules")
          )}
        </h1>
      </TitleRow>
      <p className="mkt-parental-note">
        {t("parental.time.intro")}{" "}
        {/* a home with no zone set answers its machine's own ("Local"): no name to show */}
        {data.rules.timeZone && data.rules.timeZone !== "Local" && (
          <>
            {t("parental.time.zone")} <bdi>{data.rules.timeZone}</bdi>.
          </>
        )}
      </p>

      {draft.length === 0 && <p data-testid="parental-time-none">{t("parental.time.none")}</p>}
      <ul className="mkt-parental-rules">
        {draft.map((r, i) => {
          const hasSlot = r.slotStart !== "" || r.slotEnd !== "";
          const title = t("parental.time.rule", { number: n(i + 1) });
          return (
            <li
              key={i}
              className="mkt-parental-rule"
              aria-label={title}
              data-testid="parental-rule"
            >
              <div className="mkt-parental-rule__head">
                <h2>{title}</h2>
                <Button
                  variant="ghost"
                  onClick={() => setDraft((d) => (d ? d.filter((_, j) => j !== i) : d))}
                >
                  {t("parental.time.removeRule")}
                </Button>
              </div>
              <fieldset>
                <legend>{t("parental.time.days")}</legend>
                <div className="mkt-parental-rule__days">
                  {dayNames.map((day, bit) => (
                    <label key={bit}>
                      <input
                        type="checkbox"
                        checked={(r.days & (1 << bit)) !== 0}
                        onChange={(e) =>
                          change(i, {
                            days: e.target.checked ? r.days | (1 << bit) : r.days & ~(1 << bit),
                          })
                        }
                      />
                      {day}
                    </label>
                  ))}
                </div>
              </fieldset>
              <fieldset>
                <legend>{t("parental.time.slot")}</legend>
                <div className="mkt-parental-rule__row">
                  <label>
                    <input
                      type="checkbox"
                      checked={hasSlot}
                      onChange={(e) =>
                        change(
                          i,
                          e.target.checked
                            ? { slotStart: FIRST_RULE.slotStart, slotEnd: FIRST_RULE.slotEnd }
                            : { slotStart: "", slotEnd: "" }
                        )
                      }
                    />{" "}
                    {t("parental.time.slot.on")}
                  </label>
                  {hasSlot && (
                    <>
                      <label className="mkt-field">
                        <span className="mkt-field__label">{t("parental.time.slot.from")}</span>
                        <input
                          type="time"
                          value={r.slotStart}
                          onChange={(e) => change(i, { slotStart: e.target.value })}
                        />
                      </label>
                      <label className="mkt-field">
                        <span className="mkt-field__label">{t("parental.time.slot.to")}</span>
                        <input
                          type="time"
                          value={r.slotEnd}
                          onChange={(e) => change(i, { slotEnd: e.target.value })}
                        />
                      </label>
                    </>
                  )}
                </div>
                {hasSlot && r.slotStart !== "" && r.slotEnd !== "" && r.slotEnd < r.slotStart && (
                  <p className="mkt-parental-note">{t("parental.time.slot.overnight")}</p>
                )}
              </fieldset>
              <fieldset>
                <legend>{t("parental.time.daily")}</legend>
                <div className="mkt-parental-rule__row">
                  <label>
                    <input
                      type="checkbox"
                      checked={r.dailyMinutes !== null}
                      onChange={(e) =>
                        change(i, {
                          dailyMinutes: e.target.checked ? FIRST_RULE.dailyMinutes : null,
                        })
                      }
                    />{" "}
                    {t("parental.time.daily.on")}
                  </label>
                  {r.dailyMinutes !== null && (
                    <label className="mkt-field">
                      <span className="mkt-field__label">{t("parental.time.daily.minutes")}</span>
                      <input
                        type="number"
                        inputMode="numeric"
                        min={0}
                        max={MAX_DAILY_MINUTES}
                        step={5}
                        value={Number.isFinite(r.dailyMinutes) ? r.dailyMinutes : ""}
                        onChange={(e) =>
                          change(i, {
                            dailyMinutes: e.target.value === "" ? NaN : Number(e.target.value),
                          })
                        }
                      />
                    </label>
                  )}
                </div>
              </fieldset>
              <label className="mkt-field">
                <span className="mkt-field__label">{t("parental.time.appliesTo")}</span>
                <select
                  value={r.libraryId}
                  onChange={(e) => change(i, { libraryId: e.target.value })}
                >
                  <option value="">{t("parental.time.wholeAccount")}</option>
                  {data.libraries.map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.name}
                    </option>
                  ))}
                  {r.libraryId !== "" && !data.libraries.some((l) => l.id === r.libraryId) && (
                    <option value={r.libraryId}>{libraryName(r.libraryId)}</option>
                  )}
                </select>
              </label>
              {problems[i] && (
                <p className="mkt-alert" role="alert">
                  {t(problems[i]!)}
                </p>
              )}
            </li>
          );
        })}
      </ul>

      <div className="mkt-parental-rule__row">
        <Button
          variant="secondary"
          disabled={draft.length >= MAX_TIME_RULES}
          onClick={() =>
            setDraft((d) => [
              ...(d ?? []),
              // the first rule starts at one hour a day, 07:00 to 20:00 (README Q10)
              (d ?? []).length === 0
                ? { ...FIRST_RULE }
                : { libraryId: "", days: ALL_DAYS, slotStart: "", slotEnd: "", dailyMinutes: 60 },
            ])
          }
        >
          {draft.length === 0 ? t("parental.time.addFirst") : t("parental.time.addRule")}
        </Button>
        <Button onClick={() => void save()} disabled={!dirty || invalid || saving} loading={saving}>
          {t("common.save")}
        </Button>
      </div>
      {saveError && (
        <p className="mkt-alert" role="alert">
          {saveError}
        </p>
      )}

      <h2>{t("parental.time.today")}</h2>
      {usage ? (
        <>
          <p data-testid="parental-time-today">
            {t("parental.time.watchedToday", { time: timeWords(today, words) })}
            {grantedToday > 0 &&
              ` ${t("parental.time.grantedToday", { time: timeWords(grantedToday * 60, words) })}`}
          </p>
          {usage.days.length > 0 && (
            <table className="mkt-parental-usage">
              <caption className="mkt-parental-note">{t("parental.time.usage")}</caption>
              <thead>
                <tr>
                  <th scope="col">{t("parental.time.usage.day")}</th>
                  <th scope="col">{t("parental.time.usage.watched")}</th>
                </tr>
              </thead>
              <tbody>
                {usage.days.map((d) => (
                  <tr key={d.day}>
                    <th scope="row">
                      {words.formatDate(`${d.day}T12:00:00Z`, {
                        weekday: "long",
                        day: "numeric",
                        month: "short",
                        timeZone: "UTC",
                      })}
                    </th>
                    <td>{timeWords(d.seconds, words)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      ) : (
        <p className="mkt-parental-note">{t("parental.time.usage.unknown")}</p>
      )}

      <h2>{t("parental.time.more")}</h2>
      <p className="mkt-parental-note">
        {t("parental.time.more.desc", { max: n(MAX_GRANT_MINUTES) })}
      </p>
      <div className="mkt-parental-grant">
        {GRANT_CHOICES.map((m) => (
          <Button key={m} variant="secondary" disabled={granting} onClick={() => void give(m)}>
            {t("parental.time.give", { time: timeWords(m * 60, words) })}
          </Button>
        ))}
      </div>
    </section>
  );
}
