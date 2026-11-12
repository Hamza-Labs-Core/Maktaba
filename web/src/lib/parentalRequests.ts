// Epic 57 story 57.06 on the web: what became of the thing a child asked a parent
// for, as the home says it.
//
//   GET /api/me/parental/requests   the caller's own requests, newest first
//
// useMyRequest follows one thing ("video:<id>", or ASKED_TIME for more time):
//
//   - what it shows is the home's answer. The mark this tab keeps (parentalAsked)
//     is only what is painted before the home has answered, and is rewritten from
//     every answer; a home that has no such route (an older one) leaves the mark
//     as the only thing known, as before.
//   - while the request waits, the home is asked every REQUEST_POLL_MS. It is not
//     asked while the tab is hidden (the question that came due is asked when the
//     tab shows again), once nothing waits, or after REQUEST_POLL_STOP_MS with no
//     change; "Check again" (recheck) asks at once and starts the count again.
//   - an answer is acted on once: `onApproved` runs when a request seen waiting
//     here turns out approved. A request that was approved before this page ever
//     saw it waiting is history (what it opened has run out, or the page would
//     not be showing), so it reads as nothing asked.
//
// Everything is one account's and one thing's: a change of either starts from
// nothing, and an answer that was on its way for the last one is dropped.
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "./api";
import { fetchMyRequests, type AskRequest, type MyRequest } from "./familyApi";
import { ASKED_TIME, forgetAsked, noteAsked, useAsked } from "./parentalAsked";

/** How often the home is asked while a request waits. */
export const REQUEST_POLL_MS = 15_000;
/** With no change for this long, the asking stops until Check again. */
export const REQUEST_POLL_STOP_MS = 30 * 60_000;

/** What became of the latest request for a thing. `none`: nothing was asked. */
export type AskState = "none" | "pending" | "approved" | "denied" | "expired";

export interface MyRequestState {
  state: AskState;
  /** the home took a request for this thing just now (the answer of the ask) */
  sent: (request: AskRequest) => void;
  /** asks the home now, without running `onApproved` (the caller acts itself) */
  recheck: () => void;
}

const rechecks = new Set<() => void>();

/** Asks the home again for every request a page follows (a tap on Check again). */
export function recheckMyRequests(): void {
  for (const r of [...rechecks]) r();
}

const isTime = (what: string) => what === ASKED_TIME;

/** The latest request for `what` in the home's list (newest first), or null. */
export function latestFor(requests: MyRequest[], what: string): MyRequest | null {
  return (
    requests.find((r) =>
      isTime(what) ? r.subjectKind === "time" : `${r.subjectKind}:${r.subjectId}` === what
    ) ?? null
  );
}

const past = (iso: string, now: number) => {
  const at = Date.parse(iso);
  return Number.isFinite(at) && at <= now;
};

/**
 * What a request is to the page now. `seen`: this page saw it waiting (or its
 * mark did) before it was approved.
 */
export function stateOf(r: MyRequest | null, what: string, seen: boolean, now: number): AskState {
  if (!r) return "none";
  switch (r.status) {
    case "pending":
      return past(r.expiresAt, now) ? "expired" : "pending";
    case "approved":
      return seen ? "approved" : "none";
    case "denied":
      // a no to more time is about its day: the next day asks afresh
      return isTime(what) && past(r.expiresAt, now) ? "none" : "denied";
    default:
      return "expired";
  }
}

interface Known {
  /** the account and thing this is about */
  key: string;
  latest: MyRequest | null;
  /** the approval was seen to come while this page waited */
  seen: boolean;
}

/**
 * The home's word on this account's latest request for `what`, kept current while
 * it waits. `on`: the account is a restricted one (nobody else is asked about).
 */
export function useMyRequest(
  userId: string | null,
  what: string,
  on: boolean,
  onApproved?: () => void
): MyRequestState {
  const key = `${userId ?? ""}\n${what}`;
  const hint = useAsked(userId, what);
  const [known, setKnown] = useState<Known | null>(null);
  // re-renders at a request's expiry
  const [, tick] = useState(0);
  const approvedRef = useRef(onApproved);
  approvedRef.current = onApproved;
  const hintRef = useRef(hint);
  hintRef.current = hint;
  const sentRef = useRef<(r: AskRequest) => void>(() => {});
  const recheckRef = useRef<() => void>(() => {});

  useEffect(() => {
    setKnown(null);
    if (!on || !userId) {
      sentRef.current = (r) => noteAsked(userId, what, r.expiresAt);
      recheckRef.current = () => {};
      return;
    }
    let alive = true;
    let timer: number | undefined;
    // the home's last word; undefined: it has not answered
    let latest: MyRequest | null | undefined;
    // this page saw the request wait: the mark says so before the home answers
    let waited = hintRef.current;
    // the request whose approval was acted on
    let acted = "";
    // the home has no such route: the mark is all there is
    let missing = false;
    // a question came due while the tab was hidden
    let due = false;
    // answers to questions asked before `gen` moved are out of date
    let gen = 0;
    let changedAt = Date.now();
    const hidden = () => document.visibilityState === "hidden";
    const waiting = () =>
      latest === undefined
        ? hintRef.current
        : stateOf(latest, what, true, Date.now()) === "pending";

    const publish = () => {
      if (latest === undefined) setKnown(null);
      else setKnown({ key, latest, seen: latest !== null && acted === latest.id });
    };

    const take = (next: MyRequest | null, quiet: boolean) => {
      if (latest === undefined || next?.id !== latest?.id || next?.status !== latest?.status)
        changedAt = Date.now();
      latest = next;
      if (next?.status === "pending" && !past(next.expiresAt, Date.now())) {
        waited = true;
        noteAsked(userId, what, next.expiresAt);
      } else {
        forgetAsked(userId, what);
      }
      const act = next?.status === "approved" && waited && acted !== next.id;
      if (act && next) acted = next.id;
      publish();
      if (act && !quiet) approvedRef.current?.();
    };

    const ask = (quiet = false) => {
      window.clearTimeout(timer);
      due = false;
      if (missing) return;
      const mine = gen;
      void fetchMyRequests()
        .then((all) => {
          if (!alive || mine !== gen) return;
          take(latestFor(all, what), quiet);
          later();
        })
        .catch((e: unknown) => {
          if (!alive || mine !== gen) return;
          if (e instanceof ApiError && (e.status === 404 || e.status === 405)) {
            // an older home: nothing to read, now or later
            missing = true;
            latest = undefined;
            publish();
            return;
          }
          // no answer: what is known stays, and the question is asked again
          later();
        });
    };

    // the next question: the poll while the request waits, and its expiry
    const later = () => {
      window.clearTimeout(timer);
      if (!waiting()) return;
      const now = Date.now();
      let wait = Infinity;
      if (now - changedAt < REQUEST_POLL_STOP_MS) wait = REQUEST_POLL_MS;
      const ends = latest ? Date.parse(latest.expiresAt) : NaN;
      if (Number.isFinite(ends)) wait = Math.min(wait, Math.max(0, ends - now) + 50);
      if (wait === Infinity) return;
      // setTimeout holds at most about 24 days; a request expires sooner
      timer = window.setTimeout(
        () => {
          tick((x) => x + 1);
          if (hidden()) due = true;
          else ask();
        },
        Math.min(wait, 0x7fffffff)
      );
    };

    const shown = () => {
      if (alive && due && !hidden()) ask();
    };
    document.addEventListener("visibilitychange", shown);

    sentRef.current = (r) => {
      noteAsked(userId, what, r.expiresAt);
      if (missing) return;
      // an answer on its way was asked before this request existed
      gen++;
      waited = true;
      changedAt = Date.now();
      latest = {
        id: r.id,
        subjectKind: r.subjectKind,
        subjectId: r.subjectId,
        minutes: r.minutes,
        status: r.status,
        createdAt: r.createdAt,
        expiresAt: r.expiresAt,
        decidedAt: null,
      };
      publish();
      later();
    };
    const recheck = () => {
      changedAt = Date.now();
      gen++;
      ask(true);
    };
    recheckRef.current = recheck;
    rechecks.add(recheck);
    ask();
    return () => {
      alive = false;
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", shown);
      rechecks.delete(recheck);
    };
  }, [userId, what, key, on]);

  const mine = known && known.key === key ? known : null;
  const state: AskState = mine
    ? stateOf(mine.latest, what, mine.seen, Date.now())
    : hint
      ? "pending"
      : "none";
  const sent = useCallback((r: AskRequest) => sentRef.current(r), []);
  const recheck = useCallback(() => recheckRef.current(), []);
  return { state, sent, recheck };
}
