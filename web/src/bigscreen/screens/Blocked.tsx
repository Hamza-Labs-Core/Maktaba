// Epic 58 story 58.14 — the big screen's parental pages (in /big, and in the TV build at
// /tv/ on Samsung and LG): Epic 57's blocked page and time's-up screen laid out for a
// remote, and Settings' "managed by" lines.
//
//   BigBlocked   in place of a video, series or collection the home answers 404 for while
//                the sign-in is limited: never a title or a picture (the home sends none).
//                Ask a parent is the first control and takes focus; while the request waits
//                the home is asked what became of it, and a yes opens the thing by itself.
//   BigTimesUp   the time's-up screen: why, when watching opens again, where it stopped,
//                and asking for 15, 30 or 60 more minutes.
//   BigManaged   Settings: "A parent manages this account." and the limit in force.
//
// No parent PIN: typing six digits on a remote is slow, and a guardian answers from
// Maktaba on their phone, as the line under the buttons says. The home filters, counts
// and refuses; these pages only say what it answered.
//
//   POST /api/me/parental/requests   {subject_kind, subject_id}; `time`: the minutes as the id
//   GET  /api/me/parental/requests   what became of the account's own requests
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useOptionalAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { useMyParental } from "../../lib/parental";
import { tierKey } from "../../lib/parentalApi";
import { ASKED_TIME } from "../../lib/parentalAsked";
import { recheckMyRequests, useMyRequest } from "../../lib/parentalRequests";
import { ASK_MINUTES, askFor, askForTime, refusalCode } from "../../lib/familyApi";
import { ApiError } from "../../lib/api";
import { useLimited, type BlockedProps } from "../../components/parental/AskParent";
import { nextWords, timeWords, type TimesUpProps } from "../../components/parental/TimeState";
import type { ParentalScreenSet } from "../../components/parental/screens";
import { Failed } from "../ui";

/** The words for a refused ask. */
function askError(e: unknown, t: (k: string) => string): string {
  if (refusalCode(e) === "too_many_requests") return t("parental.ask.tooMany");
  if (e instanceof ApiError && e.status === 404) return t("parental.ask.gone");
  return t("common.error");
}

/** The limit line: "Your account is limited to 9+." or the device's; null with no limit. */
function useLimitLine(): string | null {
  const { t } = useI18n();
  const { restricted, maxTier, deviceLimit } = useMyParental();
  if (maxTier === null) return null;
  const byDevice = deviceLimit !== null && (!restricted || deviceLimit === maxTier);
  return t(byDevice ? "parental.blocked.device" : "parental.blocked.account", {
    tier: t(tierKey(maxTier)),
  });
}

/** The page's frame: an icon, a heading, the lines and the buttons, centred. */
function Page({
  testId,
  icon,
  title,
  children,
}: {
  testId: string;
  icon: ReactNode;
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="bs-parental" data-testid={testId} role="alert">
      {icon}
      <h2 className="bs-parental__title">{title}</h2>
      {children}
    </section>
  );
}

export function BigBlocked({ kind, id, onCheck }: BlockedProps) {
  const { t } = useI18n();
  const { restricted } = useMyParental();
  const userId = useOptionalAuth()?.user?.id ?? null;
  const limit = useLimitLine();
  const request = useMyRequest(userId, `${kind}:${id}`, restricted, onCheck);
  const asked = request.state === "pending";
  const approved = request.state === "approved";
  const denied = request.state === "denied";
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function ask() {
    setBusy(true);
    setError(null);
    try {
      request.sent(await askFor(kind, id));
    } catch (e) {
      setError(askError(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Page
      testId="parental-blocked"
      icon={<LockIcon label={t("parental.blocked.a11y")} />}
      title={t("parental.blocked.title")}
    >
      {limit && <p data-testid="parental-blocked-reason">{limit}</p>}
      {asked && <p data-testid="parental-ask-waiting">{t("parental.ask.sent")}</p>}
      {approved && <p data-testid="parental-ask-approved">{t("parental.ask.approved")}</p>}
      {denied && <p data-testid="parental-ask-denied">{t("parental.ask.denied")}</p>}
      {error && <p className="bs-parental__error">{error}</p>}
      {restricted && (
        <div className="bs-parental__actions" data-bs-group="parental">
          {!asked && !approved ? (
            <button
              type="button"
              className="bs-btn bs-btn--primary"
              data-bs-focus="parental:ask"
              data-bs-autofocus
              data-testid="parental-ask"
              // never `disabled`: a disabled button drops the remote's focus
              aria-disabled={busy || undefined}
              onClick={() => !busy && void ask()}
            >
              {t(denied ? "parental.ask.again" : "parental.blocked.ask")}
            </button>
          ) : (
            <button
              type="button"
              className="bs-btn bs-btn--primary"
              data-bs-focus="parental:check"
              data-bs-autofocus
              data-testid="parental-check"
              onClick={() => {
                request.recheck();
                onCheck();
              }}
            >
              {t("parental.time.check")}
            </button>
          )}
        </div>
      )}
      {restricted && (
        <p className="bs-parental__hint" data-testid="parental-ask-on-phone">
          {t("tv.parental.askOnPhone")}
        </p>
      )}
    </Page>
  );
}

export function BigTimesUp({ reason, nextAllowedAt, savedAt, onCheck }: TimesUpProps) {
  const words = useI18n();
  const { t } = words;
  const { restricted } = useMyParental();
  const userId = useOptionalAuth()?.user?.id ?? null;
  const request = useMyRequest(userId, ASKED_TIME, restricted, onCheck);
  const asked = request.state === "pending";
  const approved = request.state === "approved";
  const denied = request.state === "denied";
  const [busy, setBusy] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const next = nextAllowedAt ? nextWords(nextAllowedAt, words) : "";

  async function ask(minutes: number) {
    setBusy(minutes);
    setError(null);
    try {
      request.sent(await askForTime(minutes));
    } catch (e) {
      setError(askError(e, t));
    } finally {
      setBusy(null);
    }
  }

  const canAsk = restricted && !asked && !approved;
  // the minute buttons go once the request is sent: the remote's focus moves to Check again
  const check = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (canAsk) return;
    const at = document.activeElement;
    if (!at || at === document.body) check.current?.focus();
  }, [canAsk]);
  return (
    <Page
      testId="parental-times-up"
      icon={<ClockIcon />}
      title={t(
        reason === "outside_slot" ? "parental.time.outside.title" : "parental.time.up.title"
      )}
    >
      {next && <p data-testid="parental-time-next">{t("parental.time.next", { time: next })}</p>}
      {savedAt && <p>{t("parental.time.saved", { position: savedAt })}</p>}
      {asked && <p data-testid="parental-ask-waiting">{t("parental.ask.sentTime")}</p>}
      {approved && <p data-testid="parental-ask-approved">{t("parental.ask.approved")}</p>}
      {denied && <p data-testid="parental-ask-denied">{t("parental.ask.denied")}</p>}
      {error && <p className="bs-parental__error">{error}</p>}
      {canAsk && <p>{t(denied ? "parental.ask.again" : "parental.time.askMore")}</p>}
      <div className="bs-parental__actions" data-bs-group="parental">
        {canAsk &&
          ASK_MINUTES.map((m, i) => (
            <button
              key={m}
              type="button"
              className={i === 0 ? "bs-btn bs-btn--primary" : "bs-btn"}
              data-bs-focus={`parental:ask-${m}`}
              data-bs-autofocus={i === 0 ? true : undefined}
              data-testid={`parental-ask-${m}`}
              aria-disabled={busy !== null || undefined}
              onClick={() => busy === null && void ask(m)}
            >
              {timeWords(m * 60, words)}
            </button>
          ))}
        {onCheck && (
          <button
            type="button"
            className="bs-btn"
            ref={check}
            data-bs-focus="parental:check"
            data-bs-autofocus={canAsk ? undefined : true}
            data-testid="parental-check"
            onClick={() => {
              recheckMyRequests();
              onCheck();
            }}
          >
            {t("parental.time.check")}
          </button>
        )}
      </div>
      {restricted && (
        <p className="bs-parental__hint" data-testid="parental-ask-on-phone">
          {t("tv.parental.askOnPhone")}
        </p>
      )}
    </Page>
  );
}

/** The screens the big screen draws in place of the web's (components/parental/screens). */
export const BIG_PARENTAL_SCREENS: ParentalScreenSet = { Blocked: BigBlocked, TimesUp: BigTimesUp };

/**
 * A screen's "not found": the blocked page for a limited sign-in (the home answers 404 for
 * what the limit hides, exactly as for what is not there), the not-found line otherwise.
 */
export function BigBlockedOr({ kind, id, onCheck, message }: BlockedProps & { message: string }) {
  return useLimited() ? (
    <BigBlocked kind={kind} id={id} onCheck={onCheck} />
  ) : (
    <Failed message={message} />
  );
}

/** Settings' lines for a limited sign-in; nothing for anyone else. */
export function BigManaged() {
  const { t } = useI18n();
  const { restricted, maxTier, deviceLimit } = useMyParental();
  if (!restricted && deviceLimit === null) return null;
  return (
    <div className="bs-managed" data-testid="big-parental-managed">
      {restricted && <p data-testid="big-parental-managed-line">{t("parental.profile.managed")}</p>}
      {restricted && maxTier !== null && (
        <p data-testid="big-parental-limit">
          {t("parental.profile.limit")}: <bdi>{t(tierKey(maxTier))}</bdi>
        </p>
      )}
      {deviceLimit !== null && (!restricted || deviceLimit !== maxTier) && (
        <p data-testid="big-parental-device">
          {t("parental.blocked.device", { tier: t(tierKey(deviceLimit)) })}
        </p>
      )}
      {restricted && <p className="bs-parental__hint">{t("tv.parental.askOnPhone")}</p>}
    </div>
  );
}

/** A padlock: nothing in it mirrors in RTL. */
function LockIcon({ label }: { label: string }) {
  return (
    <svg
      className="bs-parental__icon"
      width="72"
      height="72"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      role="img"
      aria-label={label}
    >
      <rect x="5" y="11" width="14" height="10" rx="2" />
      <path d="M8 11V7a4 4 0 0 1 8 0v4" />
    </svg>
  );
}

/** A clock face: nothing in it mirrors in RTL. */
function ClockIcon() {
  return (
    <svg
      className="bs-parental__icon"
      width="72"
      height="72"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </svg>
  );
}
