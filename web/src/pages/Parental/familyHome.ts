// A pretend home for the family pages' tests: the routes of Epic 57 the web calls,
// answering from one object a test fills in. A value that is an ApiError is the
// home's refusal; a route the object does not have answers 404, as the home does.
import { ApiError } from "../../lib/api";

export const problem = (status: number, extra: Record<string, unknown> = {}, retry?: number) =>
  new ApiError({ type: "about:blank", title: "refused", status, ...extra } as never, retry);

export interface FamilyHome {
  children?: unknown[];
  accounts?: unknown[] | ApiError;
  policy?: unknown | ApiError;
  rules?: unknown[] | ApiError;
  requests?: unknown[] | ApiError;
  activity?: (query: URLSearchParams) => unknown;
  pin?: boolean | ApiError;
  unlockers?: unknown[];
  libraries?: unknown[];
  series?: unknown[];
  videos?: unknown[];
}

const answer = (v: unknown) => (v instanceof ApiError ? Promise.reject(v) : Promise.resolve(v));

/** The GET side: `get.mockImplementation(familyGet(home))`. */
export function familyGet(h: FamilyHome) {
  return (url: string) => {
    const [path, qs = ""] = url.split("?");
    if (path === "/api/parental/children") return answer({ children: h.children ?? [] });
    if (path === "/api/admin/library-acl")
      return h.accounts instanceof ApiError
        ? answer(h.accounts)
        : answer({ users: h.accounts ?? [], libraries: [], grants: [] });
    if (path === "/api/parental/requests")
      return h.requests instanceof ApiError
        ? answer(h.requests)
        : answer({ requests: h.requests ?? [] });
    if (path === "/api/me/parental-pin")
      return h.pin instanceof ApiError ? answer(h.pin) : answer({ set: h.pin === true });
    if (path === "/api/me/parental/guardians") return answer({ guardians: h.unlockers ?? [] });
    if (path === "/api/libraries/summary") return answer({ items: h.libraries ?? [] });
    if (path === "/api/series") return answer({ items: h.series ?? [] });
    if (path === "/api/videos") return answer({ items: h.videos ?? [] });
    if (/^\/api\/users\/[^/]+\/parental$/.test(path)) return answer(h.policy ?? problem(404));
    if (path.endsWith("/parental/rules"))
      return h.rules instanceof ApiError ? answer(h.rules) : answer({ rules: h.rules ?? [] });
    if (path.endsWith("/parental/activity") && h.activity)
      return answer(h.activity(new URLSearchParams(qs)));
    return answer(problem(404));
  };
}

export const CHILD = "11111111-2222-3333-4444-555555555555";

/** A policy answer as the home sends it. */
export function policyAnswer(over: Record<string, unknown> = {}) {
  return {
    policy: { user_id: CHILD, max_tier: 9, unrated: "hide", allow_only: false, version: 3 },
    access: "admin",
    guardians: [],
    signals: [],
    ...over,
  };
}
