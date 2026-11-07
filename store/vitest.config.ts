import { cloudflareTest, readD1Migrations } from "@cloudflare/vitest-pool-workers";
import { defineConfig } from "vitest/config";
import { testSigner } from "./scripts/gen-vectors";
import { signEnvelope, toB64 } from "./signing/sig";

// The store's tests: the signer on the shared vectors in Node, and the Worker in Miniflare
// (D1, R2, Durable Objects, Queues), with TEST keys from public labels only.
async function workers() {
  const root = await testSigner("root-1");
  const store = await testSigner("store-1");
  const day = 24 * 3600_000;
  const iso = (ms: number) => new Date(Date.now() + ms).toISOString().replace(/\.\d{3}Z$/, "Z");
  const rootText = await signEnvelope("root", { version: 1, threshold: 1, keys: [{ id: root.id, public: root.public }] }, root);
  const storeKeys = await signEnvelope(
    "store-keys",
    {
      version: 1,
      expires: iso(300 * day),
      keys: [{ id: store.id, public: store.public, use: "store", not_before: iso(-30 * day), not_after: iso(365 * day) }],
    },
    root,
  );
  const seed = new Uint8Array(
    await crypto.subtle.digest("SHA-256", new TextEncoder().encode("maktaba-plugins TEST KEY, never for production: store-1")),
  );
  return cloudflareTest({
    wrangler: { configPath: "./wrangler.jsonc" },
    miniflare: {
      bindings: {
        SIGNUP_OPEN: "true",
        SUBMISSIONS_OPEN: "true",
        UPLOADS: "worker",
        MAIL_PROVIDER: "outbox",
        PLUGINS_ROOT: rootText,
        STORE_SIGNING_KEY: toB64(seed),
        MFA_ENC_KEY: toB64(crypto.getRandomValues(new Uint8Array(32))),
        BOOTSTRAP_ADMIN_TOKEN: "test-bootstrap-token-0123456789abcdef",
        TEST_STORE_KEYS: storeKeys,
        TEST_MIGRATIONS: JSON.stringify({
          accounts: await readD1Migrations("./migrations/accounts"),
          catalog: await readD1Migrations("./migrations/catalog"),
          installs: await readD1Migrations("./migrations/installs"),
        }),
      },
    },
  });
}

export default defineConfig(async () => ({
  test: {
    projects: [
      { test: { name: "node", include: ["signing/test/**/*.test.ts", "test/*.test.ts"] } },
      { esbuild: { jsx: "automatic" }, test: { name: "console", environment: "jsdom", include: ["console/test/**/*.test.tsx"] } },
      {
        plugins: [await workers()],
        test: { name: "workers", include: ["test/workers/**/*.test.ts"], setupFiles: ["./test/workers/setup.ts"] },
      },
    ],
  },
}));
