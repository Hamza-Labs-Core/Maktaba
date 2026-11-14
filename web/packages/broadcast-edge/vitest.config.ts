import { readFileSync } from "node:fs";
import { cloudflareTest } from "@cloudflare/vitest-pool-workers";
import { defineConfig } from "vitest/config";

// The broadcast edge's tests run the Worker and its Durable Objects in Miniflare (workerd, local
// R2, the Cache API), with TEST keys from public labels only:
//   - the cloud's keys: the Go side's vectors (cloud/internal/broadcast/testdata/vectors.json);
//     the tests derive the same seeds from the same labels to sign their own tokens;
//   - the Stream signing key: Epic 34's TEST key (web/packages/tvsite/src/testdata/parity.json);
//   - the Stream API: a fake Worker bound as STREAM_API (no call leaves the machine).
const vectors = readFileSync("../../../cloud/internal/broadcast/testdata/vectors.json", "utf8");
const parity = JSON.parse(readFileSync("../tvsite/src/testdata/parity.json", "utf8")) as {
  kid: string;
  jwk: string;
};

// A fake Stream API: POST …/live_inputs makes an input; GET /__calls says how many were made.
const fakeStream = `
let made = 0;
export default {
  async fetch(request) {
    const url = new URL(request.url);
    if (url.pathname === "/__calls") return Response.json({ made });
    if (request.method === "POST" && url.pathname.endsWith("/stream/live_inputs")) {
      if (request.headers.get("Authorization") !== "Bearer test-stream-token") return Response.json({ success: false }, { status: 403 });
      made++;
      const uid = "input" + made.toString().padStart(28, "0");
      return Response.json({ success: true, result: { uid, rtmps: { url: "rtmps://live.cloudflare.com:443/live/", streamKey: "key-" + uid } } });
    }
    return Response.json({ success: false }, { status: 404 });
  },
};`;

export default defineConfig({
  test: {
    // 10 minutes of segments for 200 viewers (T06) runs a few seconds on a quiet runner
    testTimeout: 120_000,
    include: ["test/**/*.test.ts"],
  },
  plugins: [
    cloudflareTest({
      wrangler: { configPath: "./wrangler.toml" },
      miniflare: {
        bindings: {
          BROADCAST_ENV: "test",
          BROADCAST_DELIVERY: "r2",
          BROADCAST_GLOBAL_OFF: "0",
          CLOUD_PUBKEYS: (JSON.parse(vectors) as { cloud_pubkeys: string }).cloud_pubkeys,
          // a TEST value: 32 bytes, base64url
          VIEWER_TOKEN_KEY: "dGVzdC12aWV3ZXIta2V5LW5ldmVyLWZvci1wcm9kdWN0",
          INGEST_HOST: "ingest.maktaba.tv",
          LIVE_HOST: "live.maktaba.tv",
          WATCH_HOST: "watch.maktaba.tv",
          STREAM_ACCOUNT_ID: "test-account",
          STREAM_API_TOKEN: "test-stream-token",
          STREAM_CUSTOMER_CODE: "testcode",
          STREAM_KEY_ID: parity.kid,
          STREAM_KEY_JWK: parity.jwk,
          TEST_VECTORS: vectors,
        },
        serviceBindings: { STREAM_API: "fake-stream" },
        workers: [
          {
            name: "fake-stream",
            modules: true,
            script: fakeStream,
            compatibilityDate: "2026-08-15",
          },
        ],
      },
    }),
  ],
});
