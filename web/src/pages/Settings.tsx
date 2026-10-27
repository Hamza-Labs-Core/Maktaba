// Story 11.6 — Settings. Account, Sessions, Appearance and Privacy are the
// viewer's own panels (pages/Profile/Preferences, Epic 42.5), shared with the
// player's Preferences page; the rest are server pages.
//
//   Account     — sign-out-everywhere (POST /api/auth/logout-all), a link
//                 to personal access tokens (/account/tokens, 11.13) and
//                 the diagnostics bundle
//   Sessions    — a link to the per-session list (/account/sessions, 11.14)
//   Appearance  — theme light/dark/system (11.8), language en/ar (11.12),
//                 density comfortable/compact (persisted)
//   Privacy     — tracking switch, recent history, activity timeline (29.4)
//   Advanced    — Experiments: beta cohorts the user may join (16.6)
//   Storage & discovery — server.toml's path map, roots, mdns, addresses,
//                 public URLs and a restart (admins; Epic 45)
//   Subscription — the plan, users against seats, the license, Maktaba
//                 Cloud (Epic 16; admins manage it, viewers read it)
//   About       — app version and updates (Epic 28)
import { useEffect, useRef, useState } from "react";
import { Navigate, NavLink, Route, Routes } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useAuth } from "../lib/auth";
import { lookupMessage, useI18n } from "../lib/i18n";
import { ModelsSection } from "./Settings/ModelsSection";
import { RulesSection } from "./Settings/RulesSection";
import { UpdatesSettings } from "./Settings/UpdatesSettings";
import { AppUpdateSettings } from "./Settings/AppUpdateSettings";
import { HomeNameSettings } from "./Settings/HomeNameSettings";
import { ExperimentsSection } from "./Settings/ExperimentsSection";
import { ServerUsageData } from "./Settings/ServerUsageData";
import { SubscriptionSettings } from "./Settings/SubscriptionSettings";
import { StorageSettings } from "./Settings/StorageSettings";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../lib/api";
import { AccountPanel, AppearancePanel, PrivacyPanel, SessionsPanel } from "./Profile/Preferences";
import {
  fetchUpdateStatus,
  updateAvailable,
  useUpdateStatus,
  type UpdateStatus,
} from "../lib/updates";
import { Markdown } from "../lib/markdown";
import { TitleRow } from "../help/HelpLink";

export function Settings() {
  const { t } = useI18n();
  const { user } = useAuth();
  const hasUpdate = updateAvailable(useUpdateStatus());
  return (
    <section className="mkt-page mkt-settings">
      <TitleRow>
        <h1>{t("nav.settings")}</h1>
      </TitleRow>
      <nav className="mkt-settings__nav" aria-label={t("nav.settings")}>
        {/* NavLink marks the open tab with aria-current="page". The links are
            absolute: this page renders under the /settings/* splat route, where
            React Router 7 resolves a relative link against the whole URL. */}
        <NavLink to="/settings" end>
          {t("settings.section.account")}
        </NavLink>
        <NavLink to="/settings/sessions">{t("settings.section.sessions")}</NavLink>
        <NavLink to="/settings/appearance">{t("settings.section.appearance")}</NavLink>
        <NavLink to="/settings/privacy">{t("settings.section.privacy")}</NavLink>
        <NavLink to="/settings/models">{t("settings.section.models")}</NavLink>
        <NavLink to="/settings/rules">{t("settings.section.rules")}</NavLink>
        <NavLink to="/settings/advanced">{t("settings.section.advanced")}</NavLink>
        {user?.is_admin && (
          <NavLink to="/settings/storage">{t("settings.section.storage")}</NavLink>
        )}
        <NavLink to="/settings/subscription">{t("settings.section.subscription")}</NavLink>
        <NavLink
          to="/settings/about"
          className="mkt-settings__navlink"
          aria-describedby={hasUpdate ? "mkt-settings-update-badge" : undefined}
        >
          {t("settings.section.about")}
          {hasUpdate && (
            <span className="mkt-update-dot" aria-hidden="true" data-testid="update-badge" />
          )}
        </NavLink>
        {hasUpdate && (
          <span id="mkt-settings-update-badge" hidden>
            {t("update.badge")}
          </span>
        )}
      </nav>
      <Routes>
        <Route index element={<AccountTab />} />
        <Route path="sessions" element={<SessionsPanel />} />
        <Route path="appearance" element={<AppearancePanel />} />
        <Route path="privacy" element={<PrivacyTab />} />
        <Route path="models" element={<ModelsSection />} />
        <Route path="rules" element={<RulesSection />} />
        <Route path="advanced" element={<AdvancedTab />} />
        <Route path="storage" element={<StorageSettings />} />
        <Route path="subscription" element={<SubscriptionSettings />} />
        <Route path="about" element={<AboutTab />} />
        {/* an unknown tab (/settings/nope) opens Account instead of an empty panel */}
        <Route path="*" element={<Navigate to="/settings" replace />} />
      </Routes>
    </section>
  );
}

// Account and Privacy are the viewer's panels (Profile/Preferences, which the player
// shows too) with the admins' parts added here: the home's name, the server's usage
// data. The API refuses non-admins on both whatever the page shows.
function AccountTab() {
  const { user } = useAuth();
  return (
    <AccountPanel>
      {user?.is_admin && (
        <>
          <hr />
          <HomeNameSettings />
        </>
      )}
    </AccountPanel>
  );
}

function PrivacyTab() {
  const { user } = useAuth();
  return <PrivacyPanel serverUsage={user?.is_admin ? <ServerUsageData /> : undefined} />;
}

// VersionInfo mirrors GET /api/system/version (Epic 28 fields included).
interface VersionInfo {
  version: string;
  commit?: string;
  build_date?: string;
  channel?: string;
}

/**
 * The release page link, only for http(s) URLs: the value comes from the
 * server, and a `javascript:` or `data:` URL in an href would run when clicked.
 */
export function safeReleaseUrl(raw: unknown): string | null {
  if (typeof raw !== "string" || raw.trim() === "") return null;
  try {
    const u = new URL(raw);
    return u.protocol === "http:" || u.protocol === "https:" ? u.href : null;
  } catch {
    return null; // relative or malformed
  }
}

type UpdatePhase = "idle" | "downloading" | "restarting" | "done" | "error" | "rolled_back";

// AboutTab is the version + update section (Stories 28.1/28.2/28.3/28.6).
// It shows the running build, the update channel and last-check time, an
// "available" card with the release notes rendered as (safe) Markdown,
// and — for admins on a self-updatable install — a one-click "Update
// now". A container that cannot update itself (the stack built from source)
// is known from the status (`install: "docker"` with its instructions), so
// its `docker compose pull` instruction shows up front instead of a button
// that can't work; the Docker image updates itself (`install: "docker"`, no
// instructions) and keeps the button. An older server's 409 instruction still
// shows after a click. A self-update the server undid (rolled_back) is reported.
function AdvancedTab() {
  return (
    <div className="mkt-settings__panel">
      <ExperimentsSection />
    </div>
  );
}

function AboutTab() {
  const { t, formatDate, locale } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const buildVersion = import.meta.env?.VITE_APP_VERSION ?? "dev";

  const [ver, setVer] = useState<VersionInfo | null>(null);
  const shared = useUpdateStatus();
  const [fresh, setFresh] = useState<UpdateStatus | null>(null);
  const upd = fresh ?? shared;
  const [checking, setChecking] = useState(false);
  const [phase, setPhase] = useState<UpdatePhase>("idle");
  const [dockerHint, setDockerHint] = useState<string | null>(null);
  // false once the tab unmounts: stops the post-update polling
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    api
      .get<VersionInfo>("/api/system/version")
      .then(setVer)
      .catch(() => {});
  }, []);

  const checkNow = async () => {
    setChecking(true);
    try {
      setFresh(await fetchUpdateStatus(true));
    } catch (e) {
      const detail = e instanceof ApiError ? e.message : t("common.error");
      toast.show({ tone: "error", message: detail });
    } finally {
      setChecking(false);
    }
  };

  const runUpdate = async () => {
    setPhase("downloading");
    setDockerHint(null);
    try {
      await api.post("/api/admin/system/update", { confirm: true });
      setPhase("restarting");
      // The server re-execs; poll /api/system/version until it answers
      // with a different version. Still the old one at the end: the update
      // was rolled back. Without a loaded version, the update check's
      // current_version is the one to move away from.
      const from = ver?.version || upd?.current_version || "";
      const polled = await pollVersionUntilChanged(
        from,
        upd?.latest_version ?? "",
        () => alive.current
      );
      if (!alive.current) return;
      if (polled.changed) {
        setVer(polled.info);
        setPhase("done");
        fetchUpdateStatus(true)
          .then((s) => alive.current && setFresh(s))
          .catch(() => {});
      } else {
        setPhase("rolled_back");
      }
    } catch (e) {
      if (!alive.current) return;
      if (e instanceof ApiError && e.status === 409 && e.problem.code === "updater_unavailable") {
        // the Docker image updates itself through the server's updater, not running here
        toast.show({ tone: "error", message: e.problem.detail ?? t("common.error") });
        setPhase("idle");
        return;
      }
      if (e instanceof ApiError && e.status === 409) {
        // a container that cannot update itself sends its instruction (an older server's
        // answer); any other 409 is a reason the update cannot start now, never a Docker
        // install (a recent crash, a running update, a switch of the home)
        const hint = (e.problem as unknown as { instructions?: string }).instructions;
        if (hint) {
          setDockerHint(hint);
          setPhase("idle");
          return;
        }
        const code = e.problem.code;
        const key = code ? `updates.blocker.${code}` : "";
        const reason = key && lookupMessage(locale, key) !== undefined ? t(key) : e.problem.detail;
        if (reason) {
          toast.show({ tone: "error", message: t("update.blocked", { reason }) });
          setPhase("idle");
          return;
        }
      }
      setPhase("error");
    }
  };

  const version = ver?.version ?? buildVersion;
  const channel = ver?.channel ?? upd?.channel ?? "stable";
  // instructions come only with a container that cannot update itself
  const docker = upd?.install === "docker" && upd.instructions ? upd.instructions : dockerHint;
  const showUpdateButton =
    user?.is_admin && upd?.available && phase !== "done" && phase !== "rolled_back" && !docker;
  const lastRollback = phase === "idle" ? upd?.rolled_back : undefined;
  // a failed update can be tried again; only a running one blocks the button
  const updateRunning = phase === "downloading" || phase === "restarting";
  const releaseUrl = safeReleaseUrl(upd?.release_url);

  return (
    <div className="mkt-settings__panel mkt-settings__about">
      <dl className="mkt-about__meta">
        <div>
          <dt>{t("update.current")}</dt>
          <dd>
            <strong>{version}</strong>
          </dd>
        </div>
        {ver?.commit && ver.commit !== "unknown" && (
          <div>
            <dt>{t("update.commit")}</dt>
            <dd>
              <code>{ver.commit.slice(0, 10)}</code>
            </dd>
          </div>
        )}
        {ver?.build_date && ver.build_date !== "unknown" && (
          <div>
            <dt>{t("update.buildDate")}</dt>
            <dd>{ver.build_date}</dd>
          </div>
        )}
        <div>
          <dt>{t("update.channel")}</dt>
          <dd>{t(`update.channel.${channel}`)}</dd>
        </div>
        {upd?.checked_at && (
          <div>
            <dt>{t("update.lastChecked")}</dt>
            <dd>{formatDate(upd.checked_at)}</dd>
          </div>
        )}
      </dl>

      <div className="mkt-about__actions">
        <Button variant="secondary" disabled={checking} onClick={checkNow}>
          {checking ? t("update.checking") : t("update.checkNow")}
        </Button>
      </div>

      {upd?.disabled && <p className="mkt-muted">{t("update.disabled")}</p>}

      {upd && !upd.disabled && !upd.available && (
        <p className="mkt-muted">{t("update.upToDate")}</p>
      )}

      {lastRollback && (
        <p className="mkt-error" role="status">
          {t("update.lastRolledBack", { version: lastRollback.version })}{" "}
          <span className="mkt-muted">{lastRollback.reason}</span>
        </p>
      )}

      {upd?.available && upd.latest_version && (
        <div className="mkt-about__update" role="region" aria-label={t("update.section.title")}>
          <h2>{t("update.available", { version: upd.latest_version })}</h2>
          {upd.release_notes && (
            <details className="mkt-about__notes" open>
              <summary>{t("update.banner.notes")}</summary>
              {/* Markdown as React elements: release-note HTML is shown as text, never injected. */}
              <Markdown className="mkt-about__notes-body mkt-markdown" source={upd.release_notes} />
            </details>
          )}
          {releaseUrl && (
            <p>
              <a href={releaseUrl} target="_blank" rel="noreferrer">
                {t("update.viewRelease")}
              </a>
            </p>
          )}

          {docker && (
            <div className="mkt-about__docker">
              <p>{t("update.docker.instructions")}</p>
              <pre>{docker}</pre>
              {upd.install === "docker" && upd.image && (
                <p className="mkt-muted">
                  {t("update.docker.image")} <code>{upd.image}</code>
                </p>
              )}
            </div>
          )}

          {showUpdateButton && (
            <Button variant="primary" disabled={updateRunning} onClick={runUpdate}>
              {updateRunning ? t(`update.phase.${phase}`) : t("update.updateNow")}
            </Button>
          )}

          {/* progress is announced: a live region for each phase text */}
          {phase === "downloading" && (
            <p className="mkt-muted" role="status">
              {t("update.phase.downloading")}
            </p>
          )}
          {phase === "restarting" && (
            <p className="mkt-muted" role="status">
              {t("update.phase.restarting")}
            </p>
          )}
          {phase === "done" && (
            <p className="mkt-success" role="status">
              {t("update.phase.done", { version: upd.latest_version })}
            </p>
          )}
          {phase === "error" && (
            <p className="mkt-error" role="alert">
              {t("update.phase.error")}
            </p>
          )}
          {phase === "rolled_back" && (
            <p className="mkt-error" role="alert">
              {t("update.rolledBack")}
            </p>
          )}
        </div>
      )}

      {/* after an update the refreshed status no longer offers one: say so here */}
      {phase === "done" && !upd?.available && (
        <p className="mkt-success" role="status">
          {t("update.phase.done", { version })}
        </p>
      )}

      {user?.is_admin && <UpdatesSettings />}

      <AppUpdateSettings />
    </div>
  );
}

// pollVersionUntilChanged re-fetches /api/system/version until it reports
// a version different from `from` (the server re-execs after a self-
// update), until the server reports `target` as rolled back (its new
// binary could not start and the old one was put back), until a bounded
// number of attempts elapse (changed: false, the update did not take), or
// until `keepGoing` says stop (the tab closed).
export async function pollVersionUntilChanged(
  from: string,
  target: string,
  keepGoing: () => boolean,
  opts: { attempts?: number; delayMs?: number } = {}
): Promise<{ changed: boolean; info: VersionInfo }> {
  const ATTEMPTS = opts.attempts ?? 30;
  const DELAY_MS = opts.delayMs ?? 2000;
  let last: VersionInfo = { version: from };
  for (let i = 0; i < ATTEMPTS && keepGoing(); i++) {
    await new Promise((r) => setTimeout(r, DELAY_MS));
    if (!keepGoing()) break;
    try {
      const v = await api.get<VersionInfo>("/api/system/version");
      last = v;
      if (v.version && v.version !== from) return { changed: true, info: v };
      if (target) {
        const s = await api.get<UpdateStatus>("/api/system/updates").catch(() => null);
        if (s?.rolled_back?.version === target) return { changed: false, info: v };
      }
    } catch {
      /* server mid-restart — keep polling */
    }
  }
  return { changed: false, info: last };
}
