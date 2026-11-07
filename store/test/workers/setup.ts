import { applyD1Migrations, env } from "cloudflare:test";

const e = env as unknown as Record<string, unknown> & { TEST_MIGRATIONS: string; TEST_STORE_KEYS: string; BUCKET: R2Bucket };
const m = JSON.parse(e.TEST_MIGRATIONS);
await applyD1Migrations(e.DB_ACCOUNTS as D1Database, m.accounts);
await applyD1Migrations(e.DB_CATALOG as D1Database, m.catalog);
await applyD1Migrations(e.DB_INSTALLS as D1Database, m.installs);
// the owner's store-keys.json, signed offline with a root key, on the CDN
await e.BUCKET.put("store-keys.json", e.TEST_STORE_KEYS);
