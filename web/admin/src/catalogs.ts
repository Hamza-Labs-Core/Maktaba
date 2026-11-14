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
  passkey_expired: "staff.passkey.expired",
  passkey_failed: "staff.passkey.addFailed",
  passkey_exists: "staff.passkey.exists",
  no_passkey: "staff.passkey.none",
  // the moderation queue's refusals (story 50.24)
  already_decided: "admin.moderation.error.already_decided",
  no_target: "admin.moderation.error.no_target",
  edge_channel_unknown: "admin.moderation.error.edge_channel_unknown",
  restore_window_closed: "admin.moderation.error.restore_window_closed",
  notice_incomplete: "admin.moderation.error.notice_incomplete",
  not_on_maktaba: "admin.moderation.error.not_on_maktaba",
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
