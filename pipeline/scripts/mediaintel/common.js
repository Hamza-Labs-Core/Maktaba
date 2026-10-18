// Shared by the control centre pages: helpers and the light / dark / system switch.
const $ = id => document.getElementById(id);
const esc = s => String(s ?? "").replace(/[&<>"]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));
const fmt = n => Number(n || 0).toLocaleString();
const THEME_KEY = "mi-theme";
function dur(sec) {
  if (!isFinite(sec) || sec <= 0) return "—";
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.round((sec % 3600) / 60);
  return d ? `${fmt(d)} days ${h} h` : h ? `${h} h ${m} min` : `${Math.max(1, m)} min`;
}
function applyTheme(mode) {          // "light" | "dark" | "system" (no attribute: follows the OS)
  const root = document.documentElement;
  if (mode === "light" || mode === "dark") root.setAttribute("data-theme", mode); else root.removeAttribute("data-theme");
  document.querySelectorAll("#theme button").forEach(b => b.setAttribute("aria-pressed", String(b.dataset.mode === mode)));
}
function savedTheme() { try { return localStorage.getItem(THEME_KEY) || "system"; } catch (e) { return "system"; } }
applyTheme(savedTheme());             // before first paint: this script is loaded in <head>
document.addEventListener("DOMContentLoaded", () => {
  applyTheme(savedTheme());
  document.querySelectorAll("#theme button").forEach(b => b.addEventListener("click", () => {
    try { localStorage.setItem(THEME_KEY, b.dataset.mode); } catch (e) {}
    applyTheme(b.dataset.mode);
  }));
});
async function post(path, body) {
  const r = await fetch(path, {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(body || {})});
  const j = await r.json().catch(() => ({})); if (!r.ok) throw new Error(j.error || `The server answered ${r.status}.`); return j;
}
function toast(msg, bad) { const t = $("toast"); if (!t) return; t.textContent = msg; t.className = "toast" + (bad ? " bad" : ""); t.hidden = false;
  clearTimeout(toast.h); toast.h = setTimeout(() => t.hidden = true, 6000); }
function attnRow(g, i) {              // one failure group with its Retry button
  const last = g.last ? new Date(g.last) : null;
  const parts = [g.retrying ? `${fmt(g.retrying)} will retry` : "", g.given_up ? `${fmt(g.given_up)} gave up` : "",
                 last && !isNaN(last) ? `last ${last.toLocaleDateString([], {day: "numeric", month: "short"})} ${last.toLocaleTimeString([], {hour: "2-digit", minute: "2-digit"})}` : ""].filter(Boolean);
  return `<div class="attn"><div class="what"><span class="cause" dir="auto">${esc(g.cause)}</span>
    <span class="counts">${esc(g.stage)} · ${fmt(g.count)} files · ${parts.join(" · ")}</span></div>
    <button class="btn sm retry" data-i="${i}" title="Reset the tries so the next run reads these again">Retry</button></div>`;
}
function wireRetry(host, groups, after) {   // Retry buttons: POST /api/retry, then refresh
  host.addEventListener("click", async e => {
    const b = e.target.closest(".retry"); if (!b) return; const g = groups()[+b.dataset.i]; if (!g) return;
    b.disabled = true;
    try { const j = await post("api/retry", {stage: g.stage, cause: g.cause}); toast(`${fmt(j.reset)} ${j.reset === 1 ? "file" : "files"} reset; the next run retries them.`); after(); }
    catch (err) { toast(err.message, true); b.disabled = false; }
  });
}
function themeSwitch() {
  return `<div class="seg" id="theme" role="group" aria-label="Colour theme"><button type="button" data-mode="light" aria-pressed="false">Light</button><button type="button" data-mode="dark" aria-pressed="false">Dark</button><button type="button" data-mode="system" aria-pressed="true">System</button></div>`;
}
