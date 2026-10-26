// A local stand-in for `wrangler dev` (no Cloudflare login, no download): it
// runs src/worker.ts unchanged through Vite's SSR loader, with an in-memory
// SiteControl and site/ as the ASSETS binding.
//
//   node packages/tvsite/dev/serve.mjs <vars.json> [port]      (from web/)
//
// vars.json holds the Worker's secrets and vars: HOME_HMAC, STREAM_KEY_ID,
// STREAM_KEY_JWK, and optionally STREAM_BASE_URL (a local Stream stand-in)
// and TOKEN_TTL_SECONDS. Nothing in it is printed.
import { readFile } from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import { Readable } from "node:stream";
import { fileURLToPath } from "node:url";
import { createServer as createVite } from "vite";

const here = path.dirname(fileURLToPath(import.meta.url));
const pkg = path.resolve(here, "..");
const [varsPath, portArg] = process.argv.slice(2);
if (!varsPath) {
  console.error("usage: node packages/tvsite/dev/serve.mjs <vars.json> [port]");
  process.exit(2);
}
const vars = JSON.parse(await readFile(varsPath, "utf8"));
const port = Number(portArg || 8787);

const vite = await createVite({
  root: pkg,
  configFile: false,
  appType: "custom",
  logLevel: "warn",
  server: { middlewareMode: true, hmr: false },
});
const mod = await vite.ssrLoadModule("/src/worker.ts");

const mem = new Map();
const control = new mod.SiteControl({
  storage: {
    get: async (k) => mem.get(k),
    put: async (k, v) => void mem.set(k, structuredClone(v)),
  },
});

const types = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css" };
async function asset(req) {
  let p = new URL(req.url).pathname;
  if (p === "/" || p.startsWith("/watch/")) p = "/index.html";
  const file = path.join(pkg, "site", path.normalize(p).replace(/^(\.\.[/\\])+/, ""));
  try {
    const body = await readFile(file);
    return new Response(body, {
      headers: { "Content-Type": types[path.extname(file)] || "application/octet-stream" },
    });
  } catch {
    return new Response("not found", { status: 404 });
  }
}

const env = {
  ...vars,
  SITE: {
    idFromName: (n) => n,
    get: () => ({ fetch: (i, init) => control.fetch(new Request(i, init)) }),
  },
  ASSETS: { fetch: asset },
};

http
  .createServer(async (req, res) => {
    try {
      const url = `http://${req.headers.host}${req.url}`;
      const hasBody = req.method !== "GET" && req.method !== "HEAD";
      const request = new Request(url, {
        method: req.method,
        headers: req.headers,
        body: hasBody ? Readable.toWeb(req) : undefined,
        duplex: hasBody ? "half" : undefined,
      });
      const r = await mod.default.fetch(request, env);
      res.writeHead(r.status, Object.fromEntries(r.headers));
      res.end(Buffer.from(await r.arrayBuffer()));
    } catch (e) {
      res.writeHead(500).end(String(e));
    }
  })
  .listen(port, "127.0.0.1", () => console.log(`tv site on http://127.0.0.1:${port}`));
