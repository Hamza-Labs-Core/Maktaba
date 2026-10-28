// Settings -> About: what this app is. Its name, icon and what it does; the version and
// build (commit, date) and update channel; the home's name and this machine's place in it;
// the plan (the license); links (the project, help, privacy); the open-source software it
// is built on; and the version info to copy for support.
//
// Updates get one line here, from the same source and in the same words as the Updates
// page (lib/updatesView): "up to date", "update available", "updating" or "behind", and a
// link to the Updates page, where everything else about updates is.
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { BASE } from "../../lib/basePath";
import { EDITION } from "../../lib/edition";
import { copyText } from "../../lib/clipboard";
import { updateAvailable, useUpdateStatus } from "../../lib/updates";
import { updatesApi, type UpdatesOverview } from "../../lib/updatesApi";
import { homeOf, machineStatus, statusTone, type UpdateStatusWord } from "../../lib/updatesView";
import "../../styles/updates.css";

/** GET /api/system/version (Epic 28 fields, story 35.11's machine). */
export interface VersionInfo {
  version: string;
  commit?: string;
  build_sha?: string;
  build_date?: string;
  build_time?: string;
  channel?: string;
  role?: "home" | "node";
  node_id?: string;
}

/** The project's home on the web. */
export const PROJECT_URL = "https://github.com/Hamza-Labs-Core/Maktaba";

/** The open-source software Maktaba is built on, with its license (names stay as they are). */
export const CREDITS: { name: string; license: string; url: string }[] = [
  { name: "Go", license: "BSD-3-Clause", url: "https://go.dev" },
  { name: "PostgreSQL", license: "PostgreSQL License", url: "https://www.postgresql.org" },
  { name: "SQLite", license: "Public domain", url: "https://sqlite.org" },
  { name: "FFmpeg", license: "LGPL-2.1+", url: "https://ffmpeg.org" },
  { name: "Whisper", license: "MIT", url: "https://github.com/openai/whisper" },
  { name: "chi", license: "MIT", url: "https://github.com/go-chi/chi" },
  { name: "React", license: "MIT", url: "https://react.dev" },
  { name: "React Router", license: "MIT", url: "https://reactrouter.com" },
  { name: "hls.js", license: "Apache-2.0", url: "https://github.com/video-dev/hls.js" },
  { name: "Tauri", license: "Apache-2.0 / MIT", url: "https://tauri.app" },
  { name: "IBM Plex Sans Arabic", license: "OFL-1.1", url: "https://github.com/IBM/plex" },
  { name: "Amiri", license: "OFL-1.1", url: "https://github.com/aliftype/amiri" },
];

/** A build date as the API sends it (Unix seconds, or a date) as a date; "" when unknown. */
export function buildDateOf(raw: string | undefined, format: (d: Date) => string): string {
  if (!raw || raw === "unknown") return "";
  const d = /^\d{9,11}$/.test(raw) ? new Date(Number(raw) * 1000) : new Date(raw);
  return Number.isNaN(d.getTime()) ? raw : format(d);
}

/** The version info to paste in a support request (English, one fact a line). */
export function supportText(facts: [string, string][]): string {
  return facts
    .filter(([, v]) => v)
    .map(([k, v]) => `${k}: ${v}`)
    .join("\n");
}

/** This machine's update status, as the Updates page says it. */
function useStatusLine(isAdmin: boolean, ver: VersionInfo | null) {
  const sys = useUpdateStatus();
  const [ov, setOv] = useState<UpdatesOverview | null>(null);
  useEffect(() => {
    if (!isAdmin) return;
    let alive = true;
    updatesApi
      .overview()
      .then((o) => alive && o && setOv(o))
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [isAdmin]);
  if (ov) {
    const me =
      (ver?.role === "node" && (ov.machines ?? []).find((m) => m.id === ver.node_id)) || homeOf(ov);
    const status = machineStatus(me, ov);
    const version =
      status === "update_available"
        ? (ov.plan?.target?.version ?? "")
        : status === "updating"
          ? (me.target ?? ov.run?.version ?? "")
          : status === "behind"
            ? (me.next?.version ?? ov.watermark ?? "")
            : "";
    return { status, version };
  }
  if (sys && !sys.disabled) {
    const status: UpdateStatusWord = updateAvailable(sys) ? "update_available" : "up_to_date";
    return { status, version: status === "update_available" ? (sys.latest_version ?? "") : "" };
  }
  return null;
}

export function AboutPanel() {
  const { t, formatDate, locale } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const buildVersion = import.meta.env?.VITE_APP_VERSION ?? "dev";
  const [ver, setVer] = useState<VersionInfo | null>(null);
  const [homeName, setHomeName] = useState("");
  const [tier, setTier] = useState("");
  const isAdmin = Boolean(user?.is_admin);
  const line = useStatusLine(isAdmin, ver);

  useEffect(() => {
    api
      .get<VersionInfo>("/api/system/version")
      .then((v) => v && setVer(v))
      .catch(() => {});
    api
      .get<{ home_name?: string }>("/api/auth/me")
      .then((m) => setHomeName(m?.home_name ?? ""))
      .catch(() => {});
    api
      .get<{ tier?: string }>("/api/entitlements")
      .then((e) => setTier(e?.tier ?? ""))
      .catch(() => {});
  }, []);

  // "0.3.37", as the Updates page writes versions (the API says "v0.3.37")
  const version = (ver?.version || buildVersion).replace(/^v(?=\d)/, "");
  const commit = ver?.commit && ver.commit !== "unknown" ? ver.commit : (ver?.build_sha ?? "");
  const built = buildDateOf(ver?.build_date ?? ver?.build_time, (d) => formatDate(d.toISOString()));
  const channel = ver?.channel ?? "stable";
  const role = ver?.role === "node" ? "node" : "home";
  const tierWord =
    tier === "home" || tier === "pro" || tier === "free" ? t(`subscription.tier.${tier}`) : "";
  const place = homeName
    ? t(role === "node" ? "about.place.node" : "about.place.home", { name: homeName })
    : t(role === "node" ? "about.role.node" : "about.role.home");

  const copy = async () => {
    const text = supportText([
      ["Maktaba", version],
      ["Commit", commit],
      ["Built", buildDateOf(ver?.build_date ?? ver?.build_time, (d) => d.toISOString())],
      ["Channel", channel],
      ["Machine", role],
      ["App", EDITION],
      ["Plan", tier],
      ["Browser", typeof navigator !== "undefined" ? navigator.userAgent : ""],
    ]);
    const ok = await copyText(text);
    toast.show({
      tone: ok ? "success" : "error",
      message: ok ? t("about.copied") : t("about.copyFailed"),
    });
  };

  return (
    <div className="mkt-settings__panel mkt-about" data-testid="about">
      <header className="mkt-about__hero">
        <img
          className="mkt-about__icon"
          src={`${BASE}/icons/icon-192.png`}
          alt=""
          width={64}
          height={64}
        />
        <div>
          <h2 className="mkt-about__name">{t("about.name")}</h2>
          <p className="mkt-muted">{t("about.tagline")}</p>
        </div>
      </header>

      {line && (
        <p className="mkt-about__status" data-testid="about-update-status">
          <Badge tone={statusTone(line.status)}>{t(`updates.status.${line.status}`)}</Badge>
          {line.version && (
            <span dir="ltr" className="mkt-about__status-version">
              {line.version}
            </span>
          )}
          {isAdmin && (
            <Link to="/admin/updates" data-testid="about-updates-link">
              {t("about.openUpdates")}
            </Link>
          )}
        </p>
      )}

      <dl className="mkt-about__facts">
        <div>
          <dt>{t("update.current")}</dt>
          <dd>
            <strong dir="ltr" data-testid="about-version">
              {version}
            </strong>
          </dd>
        </div>
        {(commit || built) && (
          <div>
            <dt>{t("about.build")}</dt>
            <dd>
              {commit && (
                <code dir="ltr" data-testid="about-commit">
                  {commit.slice(0, 10)}
                </code>
              )}
              {commit && built ? " · " : null}
              {built && <span data-testid="about-built">{built}</span>}
            </dd>
          </div>
        )}
        <div>
          <dt>{t("update.channel")}</dt>
          <dd>{t(`update.channel.${channel === "beta" ? "beta" : "stable"}`)}</dd>
        </div>
        <div>
          <dt>{t("about.machine")}</dt>
          <dd data-testid="about-machine">{place}</dd>
        </div>
        {tierWord && (
          <div>
            <dt>{t("about.plan")}</dt>
            <dd data-testid="about-plan">
              {tierWord}{" "}
              <Link to="/settings/subscription" className="mkt-about__aside">
                {t("about.planManage")}
              </Link>
            </dd>
          </div>
        )}
        <div>
          <dt>{t("about.app")}</dt>
          <dd>{t(`about.edition.${EDITION}`)}</dd>
        </div>
      </dl>

      <div className="mkt-updates__actions">
        <Button variant="secondary" size="sm" onClick={() => void copy()}>
          {t("about.copy")}
        </Button>
      </div>

      <section aria-labelledby="about-links" className="mkt-about__block">
        <h3 id="about-links">{t("about.links")}</h3>
        <ul className="mkt-about__links">
          <li>
            <a href={PROJECT_URL} target="_blank" rel="noreferrer">
              {t("about.link.website")}
            </a>
          </li>
          <li>
            <Link to="/help">{t("about.link.help")}</Link>
          </li>
          <li>
            <Link to="/settings/privacy">{t("about.link.privacy")}</Link>
          </li>
          <li>
            <a href={`${PROJECT_URL}/blob/main/LICENSE`} target="_blank" rel="noreferrer">
              {t("about.link.license")}
            </a>
          </li>
        </ul>
      </section>

      <section aria-labelledby="about-credits" className="mkt-about__block">
        <h3 id="about-credits">{t("about.credits")}</h3>
        <p className="mkt-muted">{t("about.creditsIntro")}</p>
        <ul className="mkt-about__credits">
          {CREDITS.map((c) => (
            <li key={c.name}>
              <a href={c.url} target="_blank" rel="noreferrer" dir="ltr" lang="en">
                {c.name}
              </a>
              <span className="mkt-muted" dir="ltr" lang="en">
                {c.license}
              </span>
            </li>
          ))}
        </ul>
      </section>
      <p className="mkt-muted mkt-about__copyright">
        {t("about.copyright", {
          year: new Date()
            .getFullYear()
            .toLocaleString(locale === "ar" ? "ar-EG" : "en", { useGrouping: false }),
        })}
      </p>
    </div>
  );
}
