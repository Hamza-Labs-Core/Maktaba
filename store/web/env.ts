// The store Worker's bindings (wrangler.jsonc). Secrets, set with `wrangler secret put` by
// the owner and never in a file: STORE_SIGNING_KEY (the online key S, a base64 Ed25519
// seed), MFA_ENC_KEY (32 bytes base64, encrypts TOTP secrets), BOOTSTRAP_ADMIN_TOKEN (once),
// MAIL_API_KEY, R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY / R2_ACCOUNT_ID (presigned uploads),
// EXPORT_PUBLIC_KEY (the owner's RSA public key for the nightly exports), and optionally
// CF_ZONE_ID / CF_PURGE_TOKEN (purging the CDN's cache of the signed lists).
import type { AccountGuard } from "../objects/account-guard";
import type { CatalogWriter } from "../objects/catalog-writer";
import type { IpBucket } from "../objects/ip-bucket";
import type { PairingCode } from "../objects/pairing-code";

export interface Env {
  STORE_ORIGIN: string;
  CDN_ORIGIN: string;
  SIGNUP_OPEN: string;
  SUBMISSIONS_OPEN: string;
  AGREEMENT_VERSION: string;
  UPLOADS: "presigned" | "worker";
  MAIL_PROVIDER: "postmark" | "outbox";
  MAIL_FROM: string;
  /** The plugin root (plugins-root.json's text); tests set a test root. Unset: the committed one. */
  PLUGINS_ROOT?: string;

  DB_ACCOUNTS: D1Database;
  DB_CATALOG: D1Database;
  DB_INSTALLS: D1Database;
  BUCKET: R2Bucket;
  PRIVATE: R2Bucket;
  ASSETS?: Fetcher;
  ACCOUNT_GUARD: DurableObjectNamespace<AccountGuard>;
  IP_BUCKET: DurableObjectNamespace<IpBucket>;
  PAIRING_CODE: DurableObjectNamespace<PairingCode>;
  CATALOG_WRITER: DurableObjectNamespace<CatalogWriter>;
  Q_CHECKS: Queue;
  Q_CATALOG: Queue;
  Q_MAIL: Queue;

  STORE_SIGNING_KEY?: string;
  MFA_ENC_KEY?: string;
  BOOTSTRAP_ADMIN_TOKEN?: string;
  MAIL_API_KEY?: string;
  R2_ACCESS_KEY_ID?: string;
  R2_SECRET_ACCESS_KEY?: string;
  R2_ACCOUNT_ID?: string;
  EXPORT_PUBLIC_KEY?: string;
  CF_ZONE_ID?: string;
  CF_PURGE_TOKEN?: string;
}

// The clock. Tests run in the Worker's own isolate and move it with setClockOffset; nothing
// else does.
let offsetMs = 0;
export function setClockOffset(ms: number): void {
  offsetMs = ms;
}
export function now(): Date {
  return new Date(Date.now() + offsetMs);
}

export const iso = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, "Z");
