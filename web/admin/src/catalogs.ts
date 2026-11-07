// The operator console's catalogs: the shared words of the cloud's apps (web/cloud/src/i18n:
// buttons, errors, paging, sign-in) under the console's own (admin.*), which no user bundle
// carries (story 50.32).
import type { Messages } from "@cloud-shared/i18n";
import userEn from "../../cloud/src/i18n/en.json";
import userAr from "../../cloud/src/i18n/ar.json";
import en from "./i18n/en.json";
import ar from "./i18n/ar.json";

export const MESSAGES: Messages = {
  en: { ...userEn, ...en },
  ar: { ...userAr, ...ar },
};
