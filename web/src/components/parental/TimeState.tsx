// Epic 57 story 57.15: the time the home says a restricted account has, on the web.
//
//   TimeLeft     "12 minutes left today", once 15 minutes or fewer remain (the
//                player) or whenever a rule covers now (Home, `always`).
//   TimeWarning  a notice five minutes and one minute before the end, saying the
//                time that is left.
//   TimesUp      the screen when the home refuses with `parental-time`: why, when
//                watching opens again, the saved place. Playback has stopped and
//                nothing asks again by itself; "Check again" is the viewer's tap.
//                A restricted account asks for more time there, or a parent types
//                their PIN (AskParent).
//   HomeTime     Home's line: the time left, or that the time is over.
//
// The web keeps no count: every number is the home's last answer (lib/parental).
import { useEffect, useState, type ReactNode } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n, type I18nState } from "../../lib/i18n";
import { pluralKey } from "../../lib/plural";
import {
  showsTimeLeft,
  useMyParental,
  useMyTime,
  warningLine,
  TIME_WARNINGS_S,
} from "../../lib/parental";
import type { MyTime, TimeReason } from "../../lib/parentalApi";
import { AskTime } from "./AskParent";
import "../../styles/parental.css";

type Words = Pick<I18nState, "t" | "n" | "locale" | "formatDate">;

/** A length of time in words: "45 minutes", "1 hour and 5 minutes", "less than a minute". */
export function timeWords(seconds: number, { t, n, locale }: Pick<Words, "t" | "n" | "locale">) {
  const total = Math.max(0, Math.floor(seconds / 60));
  if (total < 1) return t("parental.time.underMinute");
  const hours = Math.floor(total / 60);
  const minutes = total % 60;
  const h = t(pluralKey(locale, "parental.time.hours", hours), { count: n(hours) });
  const m = t(pluralKey(locale, "parental.time.minutes", minutes), { count: n(minutes) });
  if (hours === 0) return m;
  if (minutes === 0) return h;
  return t("parental.time.hoursMinutes", { hours: h, minutes: m });
}

/**
 * When watching opens again, in the viewer's words: the time alone when it is
 * today, with the weekday otherwise. The instant is the home's; only its display
 * is local.
 */
export function nextWords(nextAllowedAt: string, { formatDate }: Pick<Words, "formatDate">) {
  const at = new Date(nextAllowedAt);
  if (Number.isNaN(at.getTime())) return "";
  const sameDay = at.toDateString() === new Date().toDateString();
  return formatDate(
    at,
    sameDay
      ? { hour: "numeric", minute: "2-digit" }
      : { weekday: "long", hour: "numeric", minute: "2-digit" }
  );
}

/** "12 minutes left today"; nothing unless the answer is close to the end (or `always`). */
export function TimeLeft({ time, always = false }: { time: MyTime | null; always?: boolean }) {
  const words = useI18n();
  if (!time || !time.allowed || !time.limited || time.remainingSeconds === null) return null;
  if (!always && !showsTimeLeft(time)) return null;
  const low = time.remainingSeconds <= TIME_WARNINGS_S[0];
  return (
    <p
      className={`mkt-parental-time__left${low ? " mkt-parental-time__left--low" : ""}`}
      data-testid="parental-time-left"
    >
      {words.t("parental.time.left", { time: timeWords(time.remainingSeconds, words) })}
    </p>
  );
}

/** How long a warning stays on screen. */
export const WARNING_MS = 10_000;

/**
 * The five-minute and one-minute warnings, each shown once as the answer crosses
 * its line. The words say the time the home said is left, not the line: a video
 * opened with two and a half minutes left is not told it has five.
 */
export function TimeWarning({ time }: { time: MyTime | null }) {
  const words = useI18n();
  const [warned, setWarned] = useState<number | null>(null);
  // the seconds left when the warning came up; null: no warning on screen
  const [shown, setShown] = useState<number | null>(null);

  useEffect(() => {
    const line = warningLine(time);
    if (line === warned) return;
    // a tighter line than the last one warned of is a new warning; a looser one
    // (more time came: a grant, a new day) only lets the lines count again
    if (line !== null && (warned === null || line < warned))
      setShown(time?.remainingSeconds ?? line);
    setWarned(line);
  }, [time, warned]);

  useEffect(() => {
    if (shown === null) return;
    const id = window.setTimeout(() => setShown(null), WARNING_MS);
    return () => window.clearTimeout(id);
  }, [shown]);

  if (shown === null) return null;
  return (
    <div className="mkt-parental-time__warning" role="alert" data-testid="parental-time-warning">
      <span>{words.t("parental.time.warning", { time: timeWords(shown, words) })}</span>
      <button
        type="button"
        className="mkt-btn mkt-btn--ghost"
        onClick={() => setShown(null)}
        aria-label={words.t("common.close")}
      >
        ×
      </button>
    </div>
  );
}

export interface TimesUpProps {
  reason: TimeReason;
  nextAllowedAt: string | null;
  /** where the video stopped, already in words ("12:34"); absent on Home and live TV */
  savedAt?: string;
  /** the viewer's own tap to ask the home again (after a parent gave more time) */
  onCheck?: () => void;
  /** more actions (the player's Back) */
  action?: ReactNode;
}

/** The time's-up screen. */
export function TimesUp({ reason, nextAllowedAt, savedAt, onCheck, action }: TimesUpProps) {
  const words = useI18n();
  const { t } = words;
  const next = nextAllowedAt ? nextWords(nextAllowedAt, words) : "";
  return (
    <div
      className="mkt-parental-timesup"
      role="alert"
      data-testid="parental-times-up"
      data-reason={reason}
    >
      <ClockIcon />
      <h2>
        {t(reason === "outside_slot" ? "parental.time.outside.title" : "parental.time.up.title")}
      </h2>
      {next && <p>{t("parental.time.next", { time: next })}</p>}
      {savedAt && <p>{t("parental.time.saved", { position: savedAt })}</p>}
      <p className="mkt-parental-timesup__ask">{t("parental.time.askParent")}</p>
      <AskTime onCheck={onCheck} />
      <div className="mkt-parental-timesup__actions">
        {onCheck && (
          <Button variant="secondary" onClick={onCheck}>
            {t("parental.time.check")}
          </Button>
        )}
        {action}
      </div>
    </div>
  );
}

/** Home's line for a restricted account: the time left today, or that it is over. */
export function HomeTime() {
  const words = useI18n();
  const { restricted } = useMyParental();
  const { time, ask } = useMyTime(restricted);
  if (!restricted || !time) return null;
  if (!time.allowed) {
    return <TimesUp reason={time.reason} nextAllowedAt={time.nextAllowedAt} onCheck={ask} />;
  }
  if (!time.limited || time.remainingSeconds === null) return null;
  return (
    <div className="mkt-parental-time" aria-label={words.t("parental.time.rules")}>
      <TimeLeft time={time} always />
    </div>
  );
}

/** A clock face: nothing in it mirrors in RTL. */
function ClockIcon() {
  return (
    <svg
      width="40"
      height="40"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 7 12 12 15 14" />
    </svg>
  );
}
