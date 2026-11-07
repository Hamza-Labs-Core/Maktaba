// A local stand-in for Cloudflare's API and the live hosts, for the tools' tests: no test ever
// calls Cloudflare (story 50.34 §4).
import { createServer } from "node:http";

/** Starts a server answering from routes: {"GET /path": (req, body) => [status, json|string, headers]}. */
export async function fakeServer(routes) {
  const seen = [];
  // the routes as a list: a handler is found by comparing with each route, never looked up by
  // a name the request supplies
  const table = Object.entries(routes);
  const srv = createServer(async (req, res) => {
    let body = "";
    for await (const c of req) body += c;
    const key = `${req.method} ${req.url}`;
    seen.push({ key, host: req.headers.host, auth: req.headers.authorization, body });
    const bare = `${req.method} ${req.url.split("?")[0]}`;
    const found = table.find(([k]) => k === key) ?? table.find(([k]) => k === bare);
    const h = found ? found[1] : undefined;
    if (!h) {
      res.writeHead(404, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ success: false, errors: [{ code: 7003, message: "No route" }] }));
      return;
    }
    const [status, out, headers] = await h(req, body);
    res.writeHead(status, { "Content-Type": typeof out === "string" ? "text/html" : "application/json", ...(headers ?? {}) });
    res.end(typeof out === "string" ? out : JSON.stringify(out));
  });
  await new Promise((r) => srv.listen(0, "127.0.0.1", r));
  const { port } = srv.address();
  return { url: `http://127.0.0.1:${port}`, seen, close: () => new Promise((r) => srv.close(r)) };
}

export const ok = (result) => [200, { success: true, errors: [], result }];
export const denied = () => [403, { success: false, errors: [{ code: 10000, message: "Authentication error" }] }];
