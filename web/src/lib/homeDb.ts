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
}

/** The `home_db` of a node's raw update_state, when it reports one. */
export function homeDbOf(updateState: Record<string, unknown> | undefined | null): HomeDb | null {
  const v = updateState?.home_db;
  if (!v || typeof v !== "object" || typeof (v as { ok?: unknown }).ok !== "boolean") return null;
  return v as HomeDb;
}
