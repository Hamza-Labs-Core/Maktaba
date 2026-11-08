// The operator console's catalogs: the shared words of the cloud's apps (web/cloud/src/i18n:
// buttons, errors, paging, sign-in) under the console's own (admin.*), which no user bundle
// carries (story 50.32).
import type { Messages } from "@cloud-shared/i18n";
import userEn from "../../cloud/src/i18n/en.json";
import userAr from "../../cloud/src/i18n/ar.json";
import en from "./i18n/en.json";
import ar from "./i18n/ar.json";

// A change the console's listener refused for its Origin (403 bad_origin, story 50.32 §4)
// reads as admin.badOrigin wherever the pages show an API error (errorMessage's error.<code>).
export const MESSAGES: Messages = {
  en: { ...userEn, ...en, "error.bad_origin": en["admin.badOrigin"] },
  ar: { ...userAr, ...ar, "error.bad_origin": ar["admin.badOrigin"] },
};
