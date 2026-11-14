// A mock home for the browser tests (the web/test-cases pattern: canned answers, test data
// only), behind page.route: the LAN address, the relay, and Google's SDK replaced by the stub.
// Every request and the page's console are written to test-results/ for the CI report.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import type { Page, Route, TestInfo } from "@playwright/test";

const HERE = path.dirname(fileURLToPath(import.meta.url));
export const HOME = "https://192-168-1-20.h1.direct.maktaba.test:8444";
export const RELAY = "https://h1.relay.maktaba.test";
export const VIDEO = "00000000-0000-4000-8000-0000580700bb";
export const VIDEO2 = "00000000-0000-4000-8000-0000580700bc";
export const QUEUE = "00000000-0000-4000-8000-0000580700cc";
export const SESSION = "00000000-0000-4000-8000-0000580700dd";

const b64 = (o: unknown) => Buffer.from(JSON.stringify(o)).toString("base64url");
export const GRANT = `cast_${b64({ alg: "RS256" })}.${b64({ aud: "cast", exp: 4102444800 })}.test`;

export const TITLES = {
  en: {
    title: "The Lighthouse Keeper",
    next: "The Storm Comes In",
    series: "Harbour Tales",
    description:
      "A keeper and his daughter tend the last light on a far rock, until the winter storm cuts them off from the mainland.",
  },
  ar: {
    title: "حارس المنارة",
    next: "العاصفة تقترب",
    series: "حكايات الميناء",
    description: "حارس وابنته يرعيان آخر منارة على صخرة بعيدة، حتى تعزلهما عاصفة الشتاء عن البر.",
  },
};

const POSTER = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 600"><defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#1b2a44"/><stop offset="1" stop-color="#0b111c"/></linearGradient></defs><rect width="400" height="600" fill="url(#g)"/><circle cx="200" cy="230" r="70" fill="#e3b252" opacity="0.85"/><rect x="185" y="300" width="30" height="220" fill="#e8ecf3"/></svg>`;

export interface MockOptions {
  lang: "en" | "ar";
  /** the LAN address never answers (the TV cannot reach it) */
  lanDown?: boolean;
  /** the relay never answers either */
  relayDown?: boolean;
  /** hold the session's answer until released (the loading screen) */
  holdSession?: Promise<void>;
  /** builds.json in place of the deployed one */
  builds?: unknown;
  /** the video's title in place of the test data's (an Arabic title with Latin, story 58.15) */
  title?: string;
}

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
    headers: { "Access-Control-Allow-Origin": "*" },
  });

/** A log line as the apps' logs redact it: no `sig` and no cast grant. */
export function redact(line: string): string {
  return line.replace(/([?&]sig=)[^&\s"]+/g, "$1…").replace(/cast_[A-Za-z0-9._-]+/g, "cast_…");
}

export async function mockHome(page: Page, info: TestInfo, o: MockOptions): Promise<void> {
  const logs: string[] = [];
  const out = (name: string) => info.outputPath(name);
  page.on("console", (m) => logs.push(`[console.${m.type()}] ${m.text()}`));
  page.on("pageerror", (e) => logs.push(`[pageerror] ${e.message}`));
  page.on("requestfinished", async (r) =>
    logs.push(`[net] ${r.method()} ${r.url()} ${(await r.response())?.status() ?? "-"}`)
  );
  page.on("requestfailed", (r) =>
    logs.push(`[net] ${r.method()} ${r.url()} failed: ${r.failure()?.errorText ?? ""}`)
  );
  page.on("close", () => fs.writeFileSync(out("page.log"), `${logs.map(redact).join("\n")}\n`));

  await page.route("https://www.gstatic.com/cast/**", (route) =>
    route.fulfill({
      contentType: "text/javascript",
      body: fs.readFileSync(path.join(HERE, "caf-stub.js"), "utf8"),
    })
  );
  if (o.builds) await page.route("**/r/builds.json", (route) => json(route, o.builds));

  const t = { ...TITLES[o.lang], ...(o.title ? { title: o.title } : {}) };
  const handler = async (route: Route, base: string) => {
    const req = route.request();
    const url = new URL(req.url());
    const p = url.pathname;
    const m = req.method();
    if (m === "OPTIONS") {
      return route.fulfill({
        status: 204,
        headers: {
          "Access-Control-Allow-Origin": "*",
          "Access-Control-Allow-Headers": "Authorization, Content-Type, X-Maktaba-Client",
          "Access-Control-Allow-Methods": "GET, POST, PUT, DELETE",
        },
      });
    }
    if (p === "/api/discovery/home")
      return json(route, { id: "h1", urls: [HOME, RELAY], api_level: 1 });
    if (req.headers()["authorization"] !== `Bearer ${GRANT}`)
      return json(route, { code: "unauthorized" }, 401);
    if (p === `/api/videos/${VIDEO}`)
      return json(route, {
        id: VIDEO,
        title: t.title,
        description: t.description,
        duration_sec: 5400,
      });
    if (p === `/api/videos/${VIDEO2}`)
      return json(route, { id: VIDEO2, title: t.next, duration_sec: 2700 });
    if (p.endsWith("/chapters"))
      return json(route, [
        { start_sec: 0, end_sec: 5400, title: o.lang === "ar" ? "الوصول" : "Arrival" },
      ]);
    if (p.endsWith("/poster"))
      return route.fulfill({
        contentType: "image/svg+xml",
        body: POSTER,
        headers: { "Access-Control-Allow-Origin": "*" },
      });
    if (p === `/api/me/play-queues/${QUEUE}`)
      return json(route, {
        id: QUEUE,
        current_pos: 0,
        version: 1,
        items: [
          { pos: 0, id: VIDEO, title: t.title, series_name: t.series },
          { pos: 1, id: VIDEO2, title: t.next, series_name: t.series },
        ],
      });
    if (p === "/api/stream/sessions" && m === "POST") {
      if (o.holdSession) await o.holdSession;
      return json(
        route,
        {
          session_id: SESSION,
          mode: "remux",
          manifest_url: `${base}/stream/${SESSION}/master.m3u8?sig=test`,
          expires_at: "2099-01-01T00:00:00Z",
          duration_sec: 5400,
          current_rendition: { codec: "hevc", height: 2160 },
        },
        201
      );
    }
    if (p.endsWith("/progress")) return json(route, { accepted: true });
    return json(route, { code: "not_found" }, 404);
  };
  await page.route(`${HOME}/**`, (route) =>
    o.lanDown ? new Promise(() => {}) : handler(route, HOME)
  );
  await page.route(`${RELAY}/**`, (route) =>
    o.relayDown ? route.abort("connectionrefused") : handler(route, RELAY)
  );
}

export function customData(lang: "en" | "ar", over: Record<string, unknown> = {}) {
  return {
    home: HOME,
    urls: [HOME, RELAY],
    grant: GRANT,
    queue_id: QUEUE,
    lang,
    api_level: 1,
    ...over,
  };
}
