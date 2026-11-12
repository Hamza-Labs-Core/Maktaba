// Epic 57 family settings: the home's refusals in the app's words. The home says
// why with a short `code` (and a problem type); a page never shows its own text.
import { ApiError } from "../../lib/api";
import { problemSlug } from "../../lib/errorKind";
import { refusalCode } from "../../lib/familyApi";

type T = (key: string, vars?: Record<string, string | number>) => string;

const BY_CODE: Record<string, string> = {
  stale: "family.conflict",
  restricted_admin: "family.error.restrictedAdmin",
  restricted_guardian: "family.error.restrictedGuardian",
  no_library_access: "family.error.noLibraryAccess",
  device_limited: "family.error.deviceLimited",
  admin_only: "family.error.adminOnly",
  request_decided: "family.error.decided",
  request_expired: "family.error.expired",
  password_wrong: "family.pin.passwordWrong",
  too_many_tries: "family.pin.tooMany",
  restricted: "parental.restricted",
};

/** The catalog key of a refusal. */
export function familyErrorKey(e: unknown): string {
  const byCode = BY_CODE[refusalCode(e)];
  if (byCode) return byCode;
  if (!(e instanceof ApiError)) return "common.error";
  if (problemSlug(e.problem.type) === "cloud-session") return "family.readOnlyCloud";
  if (e.status === 403) return "family.error.forbidden";
  if (e.status === 404) return "family.error.notFound";
  if (e.status === 422) return "family.error.invalid";
  return "common.error";
}

export const familyError = (e: unknown, t: T) => t(familyErrorKey(e));

/** Whether the home refused because someone else changed the policy meanwhile. */
export const isStale = (e: unknown) => refusalCode(e) === "stale";
