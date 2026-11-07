import { readFileSync, readdirSync } from "node:fs";
import { cloudflareTest, readD1Migrations } from "@cloudflare/vitest-pool-workers";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";
import { testSigner } from "./scripts/gen-vectors";
import { signEnvelope, toB64 } from "./signing/sig";

// The store's tests: the signer on the shared vectors and paid plugins (commerce/, payments/)
// in Node, and the Worker in Miniflare
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
        // the entitlement key E (story 54.19 §6), a TEST seed from a public label
        ENTITLEMENT_SIGNING_KEY: toB64(
          new Uint8Array(
            await crypto.subtle.digest("SHA-256", new TextEncoder().encode("maktaba-plugins TEST KEY, never for production: entitlement-1")),
          ),
        ),
        MFA_ENC_KEY: toB64(crypto.getRandomValues(new Uint8Array(32))),
        BOOTSTRAP_ADMIN_TOKEN: "test-bootstrap-token-0123456789abcdef",
        TEST_STORE_KEYS: storeKeys,
        // the component corpus (checks/testdata/components, made by crates/audit's test)
        TEST_COMPONENTS: JSON.stringify(
          Object.fromEntries(
            readdirSync("./checks/testdata/components").map((f) => [
              f.replace(/\.wasm$/, ""),
              readFileSync(`./checks/testdata/components/${f}`).toString("base64"),
            ]),
          ),
        ),
        // two cases of the shared flow corpus (shared/plugins/testdata/flows, story 54.39) for
        // C12 at verification: their flow.toml and member manifests
        TEST_FLOWS: JSON.stringify(
          Object.fromEntries(
            ["cycle_three_store", "chain_two_store"].map((c) => {
              const dir = `../shared/plugins/testdata/flows/${c}`;
              const plugins = Object.fromEntries(
                readdirSync(`${dir}/plugins`).map((f) => [f.replace(/\.toml$/, ""), readFileSync(`${dir}/plugins/${f}`, "utf8")]),
              );
              return [c, { flow: readFileSync(`${dir}/flow.toml`, "utf8"), plugins }];
            }),
          ),
        ),
        RUNNER: "none",
        // the tests run the checks themselves (runChecks)
        CHECKS_FROM_QUEUE: "false",
        REVIEW_SAMPLE: "0",
        TEST_MIGRATIONS: JSON.stringify({
          accounts: await readD1Migrations("./migrations/accounts"),
          catalog: await readD1Migrations("./migrations/catalog"),
          installs: await readD1Migrations("./migrations/installs"),
          commerce: await readD1Migrations("./migrations/commerce"),
        }),
      },
    },
  });
}

// The flow check imports the plugin SDK's checker from its source (54.16 C12): its one
// dependency, smol-toml, resolves from the store's own node_modules.
const resolve = { alias: { "smol-toml": fileURLToPath(new URL("./node_modules/smol-toml/dist/index.js", import.meta.url)) } };

export default defineConfig(async () => ({
  resolve,
  test: {
    projects: [
      { resolve, test: { name: "node", include: ["checks/**/*.test.ts", "signing/test/**/*.test.ts", "test/*.test.ts", "commerce/**/*.test.ts", "payments/**/*.test.ts"] } },
      { esbuild: { jsx: "automatic" }, test: { name: "console", environment: "jsdom", include: ["console/test/**/*.test.tsx"] } },
      {
        resolve,
        plugins: [await workers()],
        // Argon2id runs in wasm for every account a test makes: slow on a loaded runner
        test: { name: "workers", include: ["test/workers/**/*.test.ts"], setupFiles: ["./test/workers/setup.ts"], testTimeout: 60_000 },
      },
    ],
  },
}));
