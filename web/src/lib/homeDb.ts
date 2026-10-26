// A joined machine's reach to the home's database, as its machine agent reports it in its
// update.state (`home_db`): GET /api/cluster passes the node's update_state through, and
// GET /api/cluster/updates gives each node machine its `home_db`.

/** Why it cannot reach the home's database. */
export type HomeDbCode =
  | "auth_failed"
  | "no_hba"
  | "refused"
  | "unreachable"
  | "no_database"
  | "tls"
  | "cert_mismatch"
  | "other";

export interface HomeDb {
  ok: boolean;
  /** why not (machines.homeDb.code.<code>) */
  code?: HomeDbCode | string;
  /** what the database driver said (English) */
  detail?: string;
  /** when it was last tried */
  at?: string;
  /** the home gave the machine new access to its database, and the machine is following it */
  healing?: boolean;
  /** how many logins in a row failed (a failure counts once seen twice: one try can meet a
   * home in the middle of a switch) */
  fails?: number;
  /** when the machine last asked the home for a new login (none came on its own) */
  asked?: string;
  /** the home's answer to that request */
  answer?: AccessAnswer;
}

/** The home's answer to a machine's request for a new login to its database. */
export interface AccessAnswer {
  ok: boolean;
  /** why not (machines.homeDb.answerCode.<code>) */
  code?: string;
  /** the home's own words (English) */
  reason?: string;
  at?: string;
}

/** The i18n key of an answer's line, and of its reason by code (`other` for a code this
 * page does not know). */
export function accessAnswerWords(
  answer: AccessAnswer,
  has: (key: string) => boolean
): { key: string; reasonKey: string } {
  if (answer.ok) return { key: "machines.homeDb.answerOk", reasonKey: "" };
  const byCode = `machines.homeDb.answerCode.${answer.code ?? "other"}`;
  return {
    key: "machines.homeDb.answer",
    reasonKey: has(byCode) ? byCode : "machines.homeDb.answerCode.other",
  };
}

/** The `home_db` of a node's raw update_state, when it reports one. */
export function homeDbOf(updateState: Record<string, unknown> | undefined | null): HomeDb | null {
  const v = updateState?.home_db;
  if (!v || typeof v !== "object" || typeof (v as { ok?: unknown }).ok !== "boolean") return null;
  const db = v as HomeDb;
  // one failed try is not shown: the machine tries again within seconds
  if (!db.ok && typeof db.fails === "number" && db.fails < 2) return null;
  return db;
}
