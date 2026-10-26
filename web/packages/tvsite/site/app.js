// The public site's page (Epic 34, first slice): the lineup, and a player for
// the channel picked. The Worker gives each play a Stream URL with a token for
// the channel's live video; before it expires the page asks again and swaps
// the token into every following request, so playback never stops.
/* global Hls */

const ar = (navigator.language || "").toLowerCase().startsWith("ar");
const t = ar
  ? { channels: "القنوات", offAir: "خارج البث الآن", tuning: "جارٍ الضبط…", error: "تعذّر التشغيل" }
  : {
      channels: "Channels",
      offAir: "Off the air right now",
      tuning: "Tuning…",
      error: "Could not play",
    };
document.documentElement.lang = ar ? "ar" : "en";
document.documentElement.dir = ar ? "rtl" : "ltr";
document.getElementById("channels-title").textContent = t.channels;

const video = document.getElementById("video");
const status = document.getElementById("status");
const list = document.getElementById("channels");
let hls = null;
let token = "";
let refresh = 0;
let current = "";

const tokenOf = (url) => new URL(url).pathname.split("/")[1] || "";

async function play(slug) {
  current = slug;
  clearTimeout(refresh);
  status.textContent = t.tuning;
  const r = await fetch("/api/v1/play", { method: "POST", body: JSON.stringify({ slug }) });
  if (current !== slug) return;
  if (!r.ok) {
    status.textContent = r.status === 503 ? t.offAir : t.error;
    refresh = setTimeout(() => play(slug), 15000);
    return;
  }
  const { url, refresh_after: after } = await r.json();
  const fresh = !token || !hls;
  token = tokenOf(url);
  refresh = setTimeout(() => renew(slug), Math.max(30, after) * 1000);
  status.textContent = "";
  if (!fresh && hls) return;
  start(url);
}

// renew asks for a new token for the same channel; the loader uses it next.
async function renew(slug) {
  const r = await fetch("/api/v1/play", { method: "POST", body: JSON.stringify({ slug }) });
  if (current !== slug) return;
  if (!r.ok) {
    refresh = setTimeout(() => renew(slug), 15000);
    return;
  }
  const { url, refresh_after: after } = await r.json();
  token = tokenOf(url);
  if (!hls) video.src = url; // native HLS (Safari): a new source
  refresh = setTimeout(() => renew(slug), Math.max(30, after) * 1000);
}

function start(url) {
  if (hls) hls.destroy();
  hls = null;
  if (window.Hls && Hls.isSupported()) {
    hls = new Hls({
      lowLatencyMode: false,
      xhrSetup(xhr, u) {
        // the token is the URL's first path segment: always the newest one
        const p = new URL(u);
        const parts = p.pathname.split("/");
        if (parts[1] && parts[1] !== token && parts[1].split(".").length === 3) {
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
}

async function lineup() {
  const r = await fetch("/api/v1/lineup");
  if (!r.ok) return;
  const { channels } = await r.json();
  list.replaceChildren(
    ...channels.map((c) => {
      const li = document.createElement("li");
      const a = document.createElement("a");
      a.href = `/watch/${encodeURIComponent(c.slug)}`;
      a.className = c.on_air ? "" : "off";
      a.setAttribute("aria-current", String(c.slug === current));
      const num = document.createElement("span");
      num.className = "num";
      num.textContent = String(c.number);
      a.append(num, c.name);
      a.addEventListener("click", (e) => {
        e.preventDefault();
        history.pushState(null, "", a.href);
        token = "";
        play(c.slug);
        lineup();
      });
      li.append(a);
      return li;
    })
  );
  return channels;
}

const fromPath = () => decodeURIComponent(location.pathname.match(/^\/watch\/([^/]+)/)?.[1] || "");
window.addEventListener("popstate", () => {
  token = "";
  if (fromPath()) play(fromPath());
});
lineup().then((channels) => {
  const first = fromPath() || channels?.find((c) => c.on_air)?.slug;
  if (first) play(first);
});
setInterval(lineup, 30000);
