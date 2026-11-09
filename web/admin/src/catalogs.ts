// The operator console's catalogs: the shared words of the cloud's apps (web/cloud/src/i18n:
// buttons, errors, paging, sign-in) under the console's own (admin.*), which no user bundle
// carries (story 50.32).
import type { Messages } from "@cloud-shared/i18n";
import userEn from "../../cloud/src/i18n/en.json";
import userAr from "../../cloud/src/i18n/ar.json";
import en from "./i18n/en.json";
import ar from "./i18n/ar.json";

// A change the console's listener refused for its Origin (403 bad_origin, story 50.32 §4), and
// the staff sign-in's own refusals (story 50.33 §9), read as the console's copy wherever the pages
// show an API error (errorMessage's error.<code>).
const ERROR_KEYS: Record<string, string> = {
  bad_origin: "admin.badOrigin",
  invalid_credentials: "staff.signIn.failed",
  mfa_failed: "staff.mfa.failed",
  mfa_required: "staff.mfa.required",
  account_locked: "staff.locked",
  ip_not_allowed: "staff.ipNotAllowed",
  admin_access_denied: "staff.forbidden",
  enrolment_link_expired: "staff.enroll.linkExpired",
  reauth_required: "staff.reauth.body",
};

function withErrors(catalog: Record<string, string>): Record<string, string> {
  const out = { ...catalog };
  for (const [code, key] of Object.entries(ERROR_KEYS)) out[`error.${code}`] = catalog[key];
  return out;
}

export const MESSAGES: Messages = {
  en: withErrors({ ...userEn, ...en }),
  ar: withErrors({ ...userAr, ...ar }),
};
