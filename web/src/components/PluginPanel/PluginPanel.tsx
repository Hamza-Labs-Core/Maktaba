// Plugin panels (Epic 54, story 54.20): a plugin's read-only view of a video, series or
// library, declared as data (ui@1) and drawn here with Maktaba's own components. Every
// value is shown as plain text: React escapes it, nothing is read as HTML or markdown, no
// value becomes a link, and the only image is the panel image the home serves
// (/api/panels/image). The API refused anything else when the plugin wrote it; this is the
// second wall.

import { Badge } from "@ds/components/Badge/Badge";
import { Link } from "react-router-dom";
import { api, ApiError } from "../../lib/api";
import { useCapabilities } from "../../lib/capabilities";
import { useI18n } from "../../lib/i18n";
import { usePageData } from "../../lib/usePageData";
import "./PluginPanel.css";

export type PanelSubject = { kind: "video" | "series" | "season" | "library"; id: string };

export interface PanelBlock {
  type: "kv" | "badges" | "chips" | "table" | "markers" | "image" | "text";
  label?: string;
  columns?: string[];
  value?: unknown;
  image?: string;
}

export interface PanelView {
  plugin_id: string;
  plugin: string;
  title: string;
  blocks: PanelBlock[];
}

/** The longest text one cell, chip or row shows; longer is cut with an ellipsis. */
export const MAX_SHOWN = 200;

const CONTROL = /[\u0000-\u001f\u007f-\u009f‎‏‪-‮⁦-⁩﻿]/g;

/** A value as the text a panel shows: strings and numbers only, controls and direction
 * overrides removed, cut to `max` characters. Anything else shows nothing. */
export function plain(v: unknown, max = MAX_SHOWN): string {
  let s: string;
  if (typeof v === "string") s = v;
  else if (typeof v === "number" && Number.isFinite(v)) s = String(v);
  else return "";
  s = s.replace(CONTROL, "");
  const chars = Array.from(s);
  return chars.length > max ? chars.slice(0, max - 1).join("") + "…" : s;
}

const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const field = (v: unknown, k: string): unknown =>
  v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>)[k] : undefined;

/** Only the home's own panel image route; never a URL a plugin wrote. */
export function safeImage(src: unknown): string | null {
  return typeof src === "string" && /^\/api\/panels\/image\?[A-Za-z0-9_.:=&%-]*$/.test(src)
    ? src
    : null;
}

const clock = (ms: number) => {
  const s = Math.floor(ms / 1000);
  const h = Math.floor(s / 3600);
  const mm = String(Math.floor((s % 3600) / 60)).padStart(h ? 2 : 1, "0");
  const ss = String(s % 60).padStart(2, "0");
  return h ? `${h}:${mm}:${ss}` : `${mm}:${ss}`;
};

function Block({ b, subject }: { b: PanelBlock; subject: PanelSubject }) {
  const label = b.label ? <h4 dir="auto">{plain(b.label, 100)}</h4> : null;
  switch (b.type) {
    case "kv":
      return (
        <div className="mkt-ppanel__block">
          {label}
          <dl className="mkt-ppanel__kv">
            {list(b.value)
              .slice(0, 20)
              .map((r, i) => (
                <div key={i}>
                  <dt dir="auto">{plain(field(r, "label"))}</dt>
                  <dd dir="auto">{plain(field(r, "value"))}</dd>
                </div>
              ))}
          </dl>
        </div>
      );
    case "badges":
      return (
        <div className="mkt-ppanel__block">
          {label}
          <div className="mkt-ppanel__row">
            {list(b.value)
              .slice(0, 10)
              .map((r, i) => {
                const tone = field(r, "tone");
                return (
                  <Badge
                    key={i}
                    tone={tone === "good" ? "success" : tone === "warning" ? "warn" : "neutral"}
                  >
                    <span dir="auto">{plain(field(r, "text"))}</span>
                  </Badge>
                );
              })}
          </div>
        </div>
      );
    case "chips":
      return (
        <div className="mkt-ppanel__block">
          {label}
          <div className="mkt-ppanel__row">
            {list(b.value)
              .slice(0, 20)
              .map((c, i) => {
                const text = plain(c);
                // a chip searches Maktaba for its text, in this app
                return text ? (
                  <Link
                    key={i}
                    className="mkt-ppanel__chip"
                    to={`/search?q=${encodeURIComponent(text)}`}
                    dir="auto"
                  >
                    {text}
                  </Link>
                ) : null;
              })}
          </div>
        </div>
      );
    case "table": {
      const cols = (b.columns ?? []).slice(0, 6);
      return (
        <div className="mkt-ppanel__block">
          {label}
          <table className="mkt-table mkt-ppanel__table">
            <thead>
              <tr>
                {cols.map((c, i) => (
                  <th key={i} dir="auto">
                    {plain(c, 100)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {list(b.value)
                .slice(0, 50)
                .map((row, i) => (
                  <tr key={i}>
                    {cols.map((_, j) => (
                      <td key={j} dir="auto">
                        {plain(list(row)[j])}
                      </td>
                    ))}
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      );
    }
    case "markers":
      if (subject.kind !== "video") return null;
      return (
        <div className="mkt-ppanel__block">
          {label}
          <ol className="mkt-chapters">
            {list(b.value)
              .slice(0, 200)
              .map((m, i) => {
                const t = field(m, "t_ms");
                if (typeof t !== "number" || !(t >= 0)) return null;
                return (
                  <li key={i}>
                    {/* seek: the player opens at the marker */}
                    <Link
                      to={`/videos/${encodeURIComponent(subject.id)}/watch?t=${Math.floor(t / 1000)}`}
                    >
                      <span className="mkt-chapters__time">{clock(t)}</span>
                      <span dir="auto">{plain(field(m, "label"))}</span>
                    </Link>
                  </li>
                );
              })}
          </ol>
        </div>
      );
    case "image": {
      const src = safeImage(b.image);
      return src ? (
        <div className="mkt-ppanel__block">
          {label}
          <img
            className="mkt-ppanel__image"
            src={src}
            alt={b.label ? plain(b.label, 100) : ""}
            loading="lazy"
          />
        </div>
      ) : null;
    }
    case "text":
      return (
        <div className="mkt-ppanel__block">
          {label}
          <p dir="auto" className="mkt-ppanel__text">
            {plain(b.value, 1000)}
          </p>
        </div>
      );
    default:
      return null;
  }
}

/** One plugin's panel. */
export function PluginPanel({ panel, subject }: { panel: PanelView; subject: PanelSubject }) {
  return (
    <section
      className="mkt-ppanel mk-card mk-card--e1"
      aria-label={plain(panel.title || panel.plugin, 100)}
    >
      <header className="mkt-ppanel__head">
        <h3 dir="auto">{plain(panel.title || panel.plugin, 100)}</h3>
        {panel.title && (
          <span className="mkt-ppanel__by" dir="auto">
            {plain(panel.plugin, 100)}
          </span>
        )}
      </header>
      {panel.blocks.map((b, i) => (
        <Block key={i} b={b} subject={subject} />
      ))}
    </section>
  );
}

/** The panels of a subject, while the home serves them; an older server (404) has none. */
export function usePanels(subject: PanelSubject): PanelView[] {
  const caps = useCapabilities();
  const { locale: lang } = useI18n();
  const key = `panels:${subject.kind}:${subject.id}:${lang}`;
  const read = usePageData<PanelView[]>(caps.panels ? key : null, () =>
    api
      .get<{ panels?: PanelView[] }>(
        `/api/panels?subject=${encodeURIComponent(`${subject.kind}:${subject.id}`)}&lang=${encodeURIComponent(lang)}`
      )
      .then((r) => (Array.isArray(r.panels) ? r.panels.slice(0, 3) : []))
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) return [];
        throw e;
      })
  );
  return caps.panels && read.err == null ? (read.data ?? []) : [];
}

/** "From plugins", collapsed, below Maktaba's own sections; nothing when there is no panel. */
export function PluginPanels({ subject }: { subject: PanelSubject }) {
  const { t } = useI18n();
  const panels = usePanels(subject);
  if (panels.length === 0) return null;
  return (
    <details className="mkt-ppanels">
      <summary>{t("plugins.panels.title")}</summary>
      {panels.map((p) => (
        <PluginPanel key={p.plugin_id} panel={p} subject={subject} />
      ))}
    </details>
  );
}
