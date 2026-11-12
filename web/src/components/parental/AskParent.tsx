// Epic 57 stories 57.05 and 57.06 on the web: what a restricted account sees where
// the home hides something or its time is over, and the two ways past it.
//
//   Blocked     the page in place of a video, series or collection the home answers
//               404 for while the sign-in is limited. It never shows a title, a
//               poster or a rating (the home sends none), only the id the page had.
//   AskTime     the time's-up screen's part: ask for more minutes today.
//   PinUnlock   a guardian types their PIN on this screen.
//
//   POST /api/me/parental/requests   {subject_kind, subject_id}; `time`: the minutes as the id
//   GET  /api/me/parental/requests   what became of the account's own requests
//   GET  /api/me/parental/guardians  who can type a PIN (none: the PIN way is not offered)
//   POST /api/me/parental/unlock     {guardian_id, pin, subject_kind, subject_id}
//
// What the page says about a request is the home's word (lib/parentalRequests): while
// it waits the home is asked again by itself; a yes opens the thing (or resumes
// watching) without a tap; a no is said, with Ask again and the PIN; an expired
// request brings the ask buttons back. What this tab remembers having asked
// (lib/parentalAsked) is only what shows until the home has answered. "Check again"
// asks the home now, for the request and for the thing itself.
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { Input } from "@ds/components/Input/Input";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useOptionalAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { useMyParental } from "../../lib/parental";
import { ASKED_TIME, forgetAsked } from "../../lib/parentalAsked";
import { useMyRequest } from "../../lib/parentalRequests";
import { tierKey } from "../../lib/parentalApi";
import {
  ASK_MINUTES,
  askFor,
  askForTime,
  fetchUnlockers,
  refusalCode,
  retryAfter,
  unlockWithPin,
  validPin,
  type Person,
  type SubjectKind,
} from "../../lib/familyApi";
import { timeWords } from "./TimeState";
import "../../styles/parental.css";

/** The words for a refused ask or unlock. */
function refusalWords(e: unknown, words: ReturnType<typeof useI18n>): string {
  const { t } = words;
  const code = refusalCode(e);
  if (code === "pin_wrong") return t("parental.pin.wrong");
  if (code === "pin_locked") {
    const wait = retryAfter(e);
    return wait !== null
      ? t("parental.pin.locked", { time: timeWords(Math.max(60, wait), words) })
      : t("parental.pin.lockedSoon");
  }
  if (code === "too_many_requests") return t("parental.ask.tooMany");
  if (e instanceof ApiError && e.status === 404) return t("parental.ask.gone");
  return t("common.error");
}

/** Who can type a PIN here; null until the home answers (asked only when `on`). */
export function useUnlockers(on: boolean): Person[] | null {
  const [who, setWho] = useState<Person[] | null>(null);
  useEffect(() => {
    setWho(null);
    if (!on) return;
    let alive = true;
    void fetchUnlockers()
      .then((g) => alive && setWho(g))
      .catch(() => alive && setWho([]));
    return () => {
      alive = false;
    };
  }, [on]);
  return who;
}

export interface PinUnlockProps {
  open: boolean;
  onClose: () => void;
  /** who can type a PIN (their guardians and the admins with one set) */
  guardians: Person[];
  /** what the PIN opens; `time`: more minutes today, chosen in the dialog */
  kind: SubjectKind | "time";
  id: string;
  onUnlocked: () => void;
}

/**
 * A guardian picks their name and types their six digits. Nothing is kept here:
 * the digits leave the page's memory when the home answers (yes or no) and when
 * the dialog closes, and the browser is told they are a one-time code, not a
 * password to offer to save on the child's device.
 */
export function PinUnlock({ open, onClose, guardians, kind, id, onUnlocked }: PinUnlockProps) {
  const words = useI18n();
  const { t, locale } = words;
  const toast = useToast();
  const [guardian, setGuardian] = useState("");
  const [pin, setPin] = useState("");
  const [minutes, setMinutes] = useState<number>(ASK_MINUTES[1]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    // closed (Cancel, Escape, unlocked) or opened again: no digits stay
    setPin("");
    if (!open) return;
    setError(null);
    setGuardian((g) => (guardians.some((x) => x.id === g) ? g : (guardians[0]?.id ?? "")));
  }, [open, guardians]);
  const close = () => {
    setPin("");
    onClose();
  };

  const sorted = [...guardians].sort((a, b) => a.username.localeCompare(b.username, locale));

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!validPin(pin) || !guardian) return;
    setBusy(true);
    setError(null);
    try {
      const got = await unlockWithPin(guardian, pin, kind, kind === "time" ? String(minutes) : id);
      toast.show({
        tone: "success",
        message:
          got.minutes !== null
            ? t("parental.pin.moreTime", { time: timeWords(got.minutes * 60, words) })
            : t("parental.pin.unlocked"),
      });
      // at once: the dialog's owner may stay on screen until the home is read again
      setPin("");
      onClose();
      onUnlocked();
    } catch (err) {
      setPin("");
      setError(refusalWords(err, words));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal open={open} onClose={close} title={t("parental.blocked.pin")}>
      <form className="mkt-form" onSubmit={(e) => void submit(e)} data-testid="parental-pin-form">
        <Select
          label={t("parental.pin.choose")}
          value={guardian}
          onChange={(e) => setGuardian(e.target.value)}
        >
          {sorted.map((g) => (
            <option key={g.id} value={g.id}>
              {g.username || g.id}
            </option>
          ))}
        </Select>
        {kind === "time" && (
          <Select
            label={t("parental.pin.minutes")}
            value={String(minutes)}
            onChange={(e) => setMinutes(Number(e.target.value))}
          >
            {ASK_MINUTES.map((m) => (
              <option key={m} value={m}>
                {timeWords(m * 60, words)}
              </option>
            ))}
          </Select>
        )}
        <Input
          label={t("parental.pin.enter")}
          type="password"
          inputMode="numeric"
          // a one-time code, so the child's browser does not offer to save it
          autoComplete="one-time-code"
          pattern="[0-9]{6}"
          maxLength={6}
          // digits read left to right in Arabic too
          dir="ltr"
          value={pin}
          onChange={(e) => setPin(e.target.value.replace(/[^0-9]/g, ""))}
        />
        {error && (
          <p className="mkt-alert" role="alert">
            {error}
          </p>
        )}
        <div className="mkt-parental-actions">
          <Button type="submit" disabled={!validPin(pin) || !guardian || busy} loading={busy}>
            {t("parental.pin.unlock")}
          </Button>
          <Button type="button" variant="secondary" onClick={close}>
            {t("common.cancel")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

export interface BlockedProps {
  kind: SubjectKind;
  id: string;
  /** asks the home for the thing again (a parent may have said yes) */
  onCheck: () => void;
  /** more actions (Back) */
  action?: ReactNode;
}

/** Whether this sign-in is limited, so a 404 may be the limit and not a missing page. */
export function useLimited(): boolean {
  const { restricted, maxTier } = useMyParental();
  return restricted || maxTier !== null;
}

/** "This isn't available", with Ask a parent and Enter parent PIN. */
export function Blocked({ kind, id, onCheck, action }: BlockedProps) {
  const words = useI18n();
  const { t } = words;
  const { restricted, maxTier, deviceLimit } = useMyParental();
  const userId = useOptionalAuth()?.user?.id ?? null;
  const what = `${kind}:${id}`;
  // what became of this account's own request for this thing; a yes opens it
  const request = useMyRequest(userId, what, restricted, onCheck);
  const asked = request.state === "pending";
  const approved = request.state === "approved";
  const denied = request.state === "denied";
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pinOpen, setPinOpen] = useState(false);
  // only a restricted account may ask or unlock (the home refuses anyone else)
  const unlockers = useUnlockers(restricted);

  async function ask() {
    setBusy(true);
    setError(null);
    try {
      request.sent(await askFor(kind, id));
    } catch (e) {
      setError(refusalWords(e, words));
    } finally {
      setBusy(false);
    }
  }

  const byDevice = deviceLimit !== null && (!restricted || deviceLimit === maxTier);
  return (
    <div className="mkt-parental-timesup" data-testid="parental-blocked">
      <LockIcon label={t("parental.blocked.a11y")} />
      <h2>{t("parental.blocked.title")}</h2>
      {maxTier !== null && (
        <p>
          {t(byDevice ? "parental.blocked.device" : "parental.blocked.account", {
            tier: t(tierKey(maxTier)),
          })}
        </p>
      )}
      {asked && (
        <p role="status" data-testid="parental-ask-waiting">
          {t("parental.ask.sent")}
        </p>
      )}
      {approved && (
        <p role="status" data-testid="parental-ask-approved">
          {t("parental.ask.approved")}
        </p>
      )}
      {denied && (
        <p role="status" data-testid="parental-ask-denied">
          {t("parental.ask.denied")}
        </p>
      )}
      {error && (
        <p className="mkt-alert" role="alert">
          {error}
        </p>
      )}
      <div className="mkt-parental-timesup__actions">
        {restricted && !asked && !approved && (
          <Button onClick={() => void ask()} disabled={busy} loading={busy}>
            {t(denied ? "parental.ask.again" : "parental.blocked.ask")}
          </Button>
        )}
        {(asked || approved) && (
          <Button
            variant="secondary"
            onClick={() => {
              request.recheck();
              onCheck();
            }}
          >
            {t("parental.time.check")}
          </Button>
        )}
        {unlockers && unlockers.length > 0 && (
          <Button variant="secondary" onClick={() => setPinOpen(true)}>
            {t("parental.blocked.pin")}
          </Button>
        )}
        {action}
      </div>
      {unlockers && unlockers.length > 0 && (
        <PinUnlock
          open={pinOpen}
          onClose={() => setPinOpen(false)}
          guardians={unlockers}
          kind={kind}
          id={id}
          onUnlocked={() => {
            // opened: this tab waits for nothing; the home says what its request is now
            forgetAsked(userId, what);
            request.recheck();
            onCheck();
          }}
        />
      )}
    </div>
  );
}

/**
 * In place of a "not found" page: the blocked page for a limited sign-in (the home
 * answers 404 for what the limit hides, exactly as for what is not there), and
 * `fallback` for everyone else.
 */
export function BlockedOr({ fallback, ...blocked }: BlockedProps & { fallback: ReactNode }) {
  return useLimited() ? <Blocked {...blocked} /> : <>{fallback}</>;
}

/** The time's-up screen's asks: more minutes from a parent, or a parent's PIN now. */
export function AskTime({ onCheck }: { onCheck?: () => void }) {
  const words = useI18n();
  const { t } = words;
  const { restricted } = useMyParental();
  const userId = useOptionalAuth()?.user?.id ?? null;
  // what became of the request for more time: it waits until a parent answers, a
  // parent's PIN, or the home's day end; a yes asks the home for the time again
  const request = useMyRequest(userId, ASKED_TIME, restricted, onCheck);
  const asked = request.state === "pending";
  const approved = request.state === "approved";
  const denied = request.state === "denied";
  const [busy, setBusy] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pinOpen, setPinOpen] = useState(false);
  const unlockers = useUnlockers(restricted);
  if (!restricted) return null;

  async function ask(minutes: number) {
    setBusy(minutes);
    setError(null);
    try {
      request.sent(await askForTime(minutes));
    } catch (e) {
      setError(refusalWords(e, words));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="mkt-parental-asktime" data-testid="parental-ask-time">
      {denied && (
        <p role="status" data-testid="parental-ask-denied">
          {t("parental.ask.denied")}
        </p>
      )}
      {asked ? (
        <p role="status" data-testid="parental-ask-waiting">
          {t("parental.ask.sentTime")}
        </p>
      ) : approved ? (
        <p role="status" data-testid="parental-ask-approved">
          {t("parental.ask.approved")}
        </p>
      ) : (
        <div
          className="mkt-parental-timesup__actions"
          role="group"
          aria-label={t(denied ? "parental.ask.again" : "parental.time.askMore")}
        >
          <span>{t(denied ? "parental.ask.again" : "parental.time.askMore")}</span>
          {ASK_MINUTES.map((m) => (
            <Button
              key={m}
              variant="secondary"
              disabled={busy !== null}
              loading={busy === m}
              onClick={() => void ask(m)}
            >
              {timeWords(m * 60, words)}
            </Button>
          ))}
        </div>
      )}
      {error && (
        <p className="mkt-alert" role="alert">
          {error}
        </p>
      )}
      {unlockers && unlockers.length > 0 && (
        <>
          <Button variant="secondary" onClick={() => setPinOpen(true)}>
            {t("parental.blocked.pin")}
          </Button>
          <PinUnlock
            open={pinOpen}
            onClose={() => setPinOpen(false)}
            guardians={unlockers}
            kind="time"
            id=""
            onUnlocked={() => {
              forgetAsked(userId, ASKED_TIME);
              request.recheck();
              onCheck?.();
            }}
          />
        </>
      )}
    </div>
  );
}

/** A padlock: nothing in it mirrors in RTL. */
function LockIcon({ label }: { label: string }) {
  return (
    <svg
      width="40"
      height="40"
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
