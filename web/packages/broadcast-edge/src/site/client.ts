// The watch pages' script and stylesheet (story 50.27 §1), served from the edge itself as
// /s/watch.js and /s/watch.css (no build step, no other origin but hls.js and Turnstile).
//
// The script reads its page's config (a JSON script tag the Worker writes), then on a channel
// page: plays through POST /api/v1/play, beats every refresh_after seconds and swaps each new
// viewer token into the playlist and segment URLs (the token is the URL's first path segment, as
// live.maktaba.tv serves it), and switches channels with up/down, PageUp/PageDown, a vertical
// swipe and number entry (digits, then Enter or a pause). Every page formats its <time> elements
// in the viewer's own time zone.
//
// The guest states come from viewerState (states.ts): its source is embedded here, so the page
// and the Worker share one mapping.
import { viewerState } from "./states";

/** The script's own code; viewerState is passed in, so this function names nothing outside it. */
function watchMain(viewerStateFn: typeof viewerState): void {
  // the edge compiles with the Workers types, not the DOM's: the page's objects are untyped here
  type Any = any;
  const win: Any = globalThis;
  const document: Any = win.document;
  const history: Any = win.history;
  const cfgEl = document.getElementById("cfg");
  const cfg = cfgEl ? (JSON.parse(cfgEl.textContent || "{}") as Record<string, unknown>) : {};
  const lang = String(cfg.lang || document.documentElement.lang || "ar");
  const words = (cfg.strings || {}) as Record<string, string>;
  const say = (key: string) => words[key] || "";

  // times in the viewer's own zone
  const fmt = new Intl.DateTimeFormat(lang === "ar" ? "ar" : "en", {
    hour: "2-digit",
    minute: "2-digit",
  });
  document.querySelectorAll("time[datetime]").forEach((el: Any) => {
    const t = Date.parse(el.getAttribute("datetime") || "");
    if (Number.isFinite(t)) el.textContent = fmt.format(new Date(t));
  });

  const video = document.getElementById("video") as Any;
  if (!video || !cfg.channel) return;

  const pub = String(cfg.publisher);
  const channels = (cfg.channels || []) as { id: string; number: number; name: string }[];
  const msg = document.getElementById("msg") as Any;
  const card = document.getElementById("card") as Any;
  const retry = document.getElementById("retry") as Any;
  const tsBox = document.getElementById("ts") as Any;
  const title = document.getElementById("title") as Any;

  type Hls = {
    loadSource(u: string): void;
    attachMedia(v: Any): void;
    destroy(): void;
  };
  const HlsCtor = win.Hls as { isSupported(): boolean; new (o: object): Hls } | undefined;

  let current = String(cfg.channel);
  let hls: Hls | null = null;
  let token = "";
  let session = "";
  let beatKey = "";
  let timer = 0;
  let epoch = 0;

  const show = (key: string, plays: boolean, canRetry: boolean) => {
    msg.textContent = key ? say(key) : "";
    card.hidden = !key;
    card.classList.toggle("over", !plays);
    retry.hidden = !canRetry;
  };

  const tokenOf = (url: string) => new URL(url).pathname.split("/")[1] || "";

  const stop = () => {
    clearTimeout(timer);
    if (hls) hls.destroy();
    hls = null;
    token = "";
    session = "";
    video.removeAttribute("src");
    video.load();
  };

  const start = (url: string) => {
    token = tokenOf(url);
    if (HlsCtor && HlsCtor.isSupported()) {
      hls = new HlsCtor({
        enableWorker: false,
        lowLatencyMode: false,
        // a slow network starts on the lowest rung; ABR climbs from there
        startLevel: 0,
        xhrSetup(xhr: Any, u: string) {
          const p = new URL(u);
          const parts = p.pathname.split("/");
          if (token && parts[1] && parts[1] !== token) {
            parts[1] = token;
            p.pathname = parts.join("/");
            xhr.open("GET", p.toString(), true);
          }
        },
      });
      hls.loadSource(url);
      hls.attachMedia(video);
    } else {
      video.src = url;
    }
    video.play().catch(() => {});
  };

  const post = async (path: string, body: object) => {
    const r = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json", "Accept-Language": lang },
      body: JSON.stringify(body),
    });
    let j: Record<string, unknown> = {};
    try {
      j = (await r.json()) as Record<string, unknown>;
    } catch {
      j = {};
    }
    return { status: r.status, j };
  };

  const beat = async (mine: number) => {
    if (mine !== epoch || !session) return;
    const { status, j } = await post(`/api/v1/play/${session}/beat`, {
      publisher: pub,
      beat_key: beatKey,
      rung: "",
      stats: {},
    });
    if (mine !== epoch) return;
    const v = viewerStateFn(status, j);
    if (status !== 200) {
      stop();
      show(v.key, false, v.retry);
      return;
    }
    show(v.key, v.plays, v.retry);
    if (typeof j.master_url === "string") {
      const next = tokenOf(j.master_url);
      // native HLS cannot swap a token in place: it gets the new source
      if (hls) token = next;
      else start(j.master_url);
    }
    timer = win.setTimeout(() => beat(mine), Number(j.refresh_after || 60) * 1000);
  };

  const verify = (then: () => void) => {
    const ts = win.turnstile as { render(el: Any, o: object): void } | undefined;
    if (!ts || !cfg.turnstile) return;
    tsBox.replaceChildren();
    ts.render(tsBox, {
      sitekey: String(cfg.turnstile),
      language: lang,
      callback: async (t: string) => {
        const r = await post("/api/v1/open/turnstile", { token: t });
        tsBox.replaceChildren();
        if (r.status === 200) then();
      },
    });
  };

  const play = async (ch: string) => {
    const mine = ++epoch;
    stop();
    current = ch;
    show("broadcast.tuning", false, false);
    const c = channels.find((x) => x.id === ch);
    if (title && c) title.textContent = `${c.number} ${c.name}`;
    const { status, j } = await post("/api/v1/play", { publisher: pub, channel: ch });
    if (mine !== epoch) return;
    const v = viewerStateFn(status, j);
    if (v.state === "verify") {
      show(v.key, false, false);
      verify(() => play(ch));
      return;
    }
    show(v.key, v.plays, v.retry);
    if (status !== 200 || !v.plays || typeof j.master_url !== "string") return;
    session = String(j.session_id || "");
    beatKey = String(j.beat_key || "");
    start(j.master_url);
    timer = win.setTimeout(() => beat(mine), Number(j.refresh_after || 60) * 1000);
  };

  const go = (ch: string) => {
    if (ch === current) return;
    history.replaceState(null, "", `/${lang}/${encodeURIComponent(pub)}/${encodeURIComponent(ch)}`);
    // a channel switch reloads the page's programme details from the server copy
    document.querySelectorAll("[data-ch]").forEach((el: Any) => {
      el.setAttribute("aria-current", String(el.getAttribute("data-ch") === ch));
    });
    void play(ch);
  };

  const step = (d: number) => {
    if (channels.length < 2) return;
    const i = channels.findIndex((x) => x.id === current);
    go(channels[(i + d + channels.length) % channels.length]!.id);
  };

  let digits = "";
  let digitTimer = 0;
  const commit = () => {
    const n = Number(digits);
    digits = "";
    const c = channels.find((x) => x.number === n);
    if (c) go(c.id);
  };

  document.addEventListener("keydown", (e: Any) => {
    const target = e.target as Any;
    if (target && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return;
    if (e.key === "ArrowUp" || e.key === "PageUp" || e.key === "ChannelUp") {
      e.preventDefault();
      step(-1);
    } else if (e.key === "ArrowDown" || e.key === "PageDown" || e.key === "ChannelDown") {
      e.preventDefault();
      step(1);
    } else if (/^[0-9]$/.test(e.key)) {
      digits = (digits + e.key).slice(-4);
      clearTimeout(digitTimer);
      digitTimer = win.setTimeout(commit, 1500);
    } else if (e.key === "Enter" && digits) {
      clearTimeout(digitTimer);
      commit();
    }
  });

  let y0 = 0;
  video.addEventListener(
    "touchstart",
    (e: Any) => {
      y0 = e.touches[0] ? e.touches[0].clientY : 0;
    },
    { passive: true }
  );
  video.addEventListener(
    "touchend",
    (e: Any) => {
      const y1 = e.changedTouches[0] ? e.changedTouches[0].clientY : y0;
      if (Math.abs(y1 - y0) > 60) step(y1 < y0 ? 1 : -1);
    },
    { passive: true }
  );

  document.querySelectorAll("a[data-ch]").forEach((a: Any) => {
    a.addEventListener("click", (e: Any) => {
      e.preventDefault();
      go(String(a.getAttribute("data-ch")));
    });
  });
  retry.addEventListener("click", () => void play(current));
  void play(current);
}

let js: string | undefined;

/**
 * clientJS is the page script: watchMain called with viewerState's own source. Built once, on
 * first use (never at module load). `__name` is defined for the bundler's keep-names helper,
 * which can appear inside a function's source.
 */
export function clientJS(): string {
  js ??= `"use strict";(function(){var __name=function(f){return f};(${watchMain.toString()})(${viewerState.toString()});})();\n`;
  return js;
}

export const CLIENT_CSS = `:root{color-scheme:dark;--bg:#0f1115;--fg:#eceef2;--mut:#a3a9b5;--card:#1a1d24;--line:#2a2f3a;--acc:#4c9aff}
*{box-sizing:border-box}html,body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,"Segoe UI","Noto Sans Arabic",sans-serif}
a{color:var(--acc)}a:focus-visible,button:focus-visible,input:focus-visible{outline:3px solid var(--acc);outline-offset:2px}
header,main,footer{max-width:72rem;margin:0 auto;padding:12px 16px}
header{display:flex;align-items:center;gap:12px;justify-content:space-between}
header .brand{display:flex;align-items:center;gap:10px;font-weight:600;font-size:1.1rem;color:var(--fg);text-decoration:none}
header img{width:40px;height:40px;object-fit:contain;border-radius:6px}
footer{color:var(--mut);font-size:.9rem;display:flex;flex-wrap:wrap;gap:8px 16px;border-top:1px solid var(--line);margin-top:24px}
h1{font-size:1.4rem;margin:.4rem 0}h2{font-size:1.1rem;margin:1.2rem 0 .4rem}
.grid{list-style:none;padding:0;margin:0;display:grid;gap:12px;grid-template-columns:repeat(auto-fill,minmax(15rem,1fr))}
.grid a{display:block;background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px;color:var(--fg);text-decoration:none;min-height:44px}
.num{display:inline-block;min-width:2.2em;color:var(--mut);font-variant-numeric:tabular-nums}
.state{font-size:.8rem;color:var(--mut)}.muted{color:var(--mut)}
.stage{position:relative;background:#000;border-radius:10px;overflow:hidden;aspect-ratio:16/9}
.stage video{width:100%;height:100%;display:block}
.card{position:absolute;inset:auto 0 0 0;padding:10px 14px;background:rgba(0,0,0,.65);text-align:center}
.card.over{inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;background:rgba(0,0,0,.85)}
.card[hidden]{display:none}
button{font:inherit;min-height:44px;padding:.5rem 1.2rem;border-radius:8px;border:0;background:var(--acc);color:#fff;cursor:pointer}
.channels{list-style:none;padding:0;display:flex;flex-wrap:wrap;gap:8px}
.channels a{display:inline-block;padding:8px 12px;border:1px solid var(--line);border-radius:8px;text-decoration:none;color:var(--fg);min-height:44px}
.channels a[aria-current=true]{border-color:var(--acc)}
.today{list-style:none;padding:0;margin:0}.today li{padding:6px 0;border-bottom:1px solid var(--line)}
.attr{font-size:.9rem;color:var(--mut)}
form{display:grid;gap:10px;max-width:32rem}input,textarea{font:inherit;padding:.6rem;border-radius:8px;border:1px solid var(--line);background:var(--card);color:var(--fg);width:100%}
@media (max-width:480px){h1{font-size:1.2rem}}
`;
