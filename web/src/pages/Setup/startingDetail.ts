// What the server waits on while it starts, in the screen's language (Epic 38
// story 38.4). The server names each process's detail by a code from a fixed
// list (detailCodes in cmd/maktaba-server/serve_appbundle.go, the table in
// specs/epics/36-mac-app/contract-update.md) with its values; the words are
// `setup.starting.detail.<code>` in src/i18n. Releases are versions ("" when
// not known), paths are whole. The values that are the programs' own English
// (`error`, `restore_error`) or a log's path (`log`) never go in the sentence:
// they are shown apart, under Details, as they are.
import { bidiIsolate } from "../../lib/i18n";
import { codedText, type Translate } from "./codedText";

/** Values shown apart from the sentence, in this order. */
export const APART = ["error", "restore_error", "log"] as const;

/** Values that are releases: "" when the server does not know it. */
const RELEASES = new Set(["release", "from", "to", "failed", "needed"]);

export interface StartingDetail {
  /** the sentence; "" when the app has no words for the code */
  text: string;
  /** the values shown apart: [their label, the value as the server has it] */
  apart: [string, string][];
}

/** The sentence for a detail code and its values, and the values shown apart. */
export function startingDetail(
  t: Translate,
  code: string | undefined,
  params: Record<string, unknown> | undefined,
  words: Record<string, string>
): StartingDetail {
  const given = params ?? {};
  const apart: [string, string][] = [];
  for (const name of APART) {
    const v = given[name];
    if (typeof v === "string" && v.trim()) apart.push([t(`setup.starting.apart.${name}`), v]);
  }
  if (!code) return { text: "", apart };
  const vars: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(given)) {
    if ((APART as readonly string[]).includes(k)) continue;
    if (typeof v !== "string") {
      vars[k] = v;
    } else if (RELEASES.has(k) && !v.trim()) {
      // db_restored's release: the one the user had before the update
      vars[k] =
        code === "db_restored" && k === "release"
          ? t("setup.starting.release.before")
          : t("setup.starting.release.unknown");
    } else {
      // a version or a whole path, kept in its own direction in a sentence
      vars[k] = bidiIsolate(v);
    }
  }
  // db_refused_older says where the database from before the update is, when it is kept
  const dump = given.dump;
  const key =
    code === "db_refused_older" && typeof dump === "string" && dump.trim() ? `${code}.dump` : code;
  return { text: codedText(t, "setup.starting.detail", key, vars, words, ""), apart };
}
