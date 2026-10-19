// /admin/extensions — Plex and dizqueTV (and future integrations) are
// extensions, not core. One card per extension: enabled toggle,
// configured status, a settings form generated from the extension's
// settings schema (secrets are write-only and shown masked), Save and
// "Test connection".
//   GET  /api/extensions
//   PUT  /api/extensions/{name}      { enabled?, settings? }
//   POST /api/extensions/{name}/test { ok, message }
// The Plex card also has "Sign in with Plex" (PlexSignin.tsx); the
// Cloudflare Stream card lists each check of its test and the remote
// viewing status (CloudflareRemote.tsx, Epic 32 story 32.6).
import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type { Extension, ExtensionSettingField } from "../../lib/queueApi";
import { useAction } from "../../components/queue/useAction";
import { PlexSignin } from "./PlexSignin";
import { CloudflareRemote, RemoteTestLines } from "./CloudflareRemote";
import type { RemoteTestResult } from "../../lib/tvRemoteApi";
import "../../styles/queue.css";

type Values = Record<string, string | number | boolean | null>;

function ExtensionCard({ ext, onSaved }: { ext: Extension; onSaved: (e: Extension) => void }) {
  const { t } = useI18n();
  const action = useAction();
  const [values, setValues] = useState<Values>(() => ({ ...ext.settings }));
  // Secrets are write-only: the draft starts empty and is only sent when typed into.
  const [secrets, setSecrets] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [lines, setLines] = useState<RemoteTestResult | null>(null);
  const [tested, setTested] = useState(0);

  useEffect(() => {
    setValues({ ...ext.settings });
    setSecrets({});
  }, [ext]);

  const set = (key: string, v: string | number | boolean | null) =>
    setValues((p) => ({ ...p, [key]: v }));

  const save = () =>
    void action
      .run("save", () => {
        const settings: Values = {};
        for (const f of ext.settings_schema) {
          if (f.type === "secret") {
            if (secrets[f.key]) settings[f.key] = secrets[f.key];
          } else if (f.key in values) {
            settings[f.key] = values[f.key];
          }
        }
        return queueApi.saveExtension(ext.name, { settings });
      })
      .then((r) => {
        if (r) {
          setMsg({ ok: true, text: t("queue.ext.saved") });
          onSaved(r);
        }
      });

  const toggle = (enabled: boolean) =>
    void action
      .run("toggle", () => queueApi.saveExtension(ext.name, { enabled }))
      .then((r) => r && onSaved(r));

  const test = () =>
    void action
      .run("test", () => queueApi.testExtension(ext.name))
      .then((r) => {
        if (r) {
          setMsg({
            ok: r.ok,
            text: t(r.ok ? "queue.ext.testOk" : "queue.ext.testBad", { message: r.message }),
          });
          const withLines = r as Partial<RemoteTestResult>;
          setLines(Array.isArray(withLines.lines) ? (withLines as RemoteTestResult) : null);
          setTested((n) => n + 1);
        }
      });

  const field = (f: ExtensionSettingField) => {
    const id = `ext-${ext.name}-${f.key}`;
    if (f.type === "boolean") {
      return (
        <label className="mkt-q-check" key={f.key}>
          <input
            id={id}
            type="checkbox"
            checked={values[f.key] === true}
            onChange={(e) => set(f.key, e.target.checked)}
          />
          <span>{f.label}</span>
        </label>
      );
    }
    return (
      <div className="mkt-q-field" key={f.key}>
        <label htmlFor={id}>
          {f.label}
          {f.required && <span className="mkt-q-muted"> ({t("queue.ext.required")})</span>}
        </label>
        {f.type === "select" ? (
          <select
            id={id}
            value={String(values[f.key] ?? "")}
            onChange={(e) => set(f.key, e.target.value)}
          >
            {(f.options ?? []).map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        ) : f.type === "secret" ? (
          <input
            id={id}
            type="password"
            autoComplete="new-password"
            value={secrets[f.key] ?? ""}
            placeholder={
              ext.secrets_set.includes(f.key)
                ? `•••••••• ${t("queue.ext.secretSaved")}`
                : t("queue.ext.secretEmpty")
            }
            onChange={(e) => setSecrets((p) => ({ ...p, [f.key]: e.target.value }))}
          />
        ) : (
          <input
            id={id}
            type={f.type === "number" ? "number" : f.type === "url" ? "url" : "text"}
            value={String(values[f.key] ?? "")}
            onChange={(e) =>
              set(f.key, f.type === "number" ? Number(e.target.value) : e.target.value)
            }
          />
        )}
        {f.help && <span className="mkt-q-muted">{f.help}</span>}
      </div>
    );
  };

  return (
    <section className="mkt-q-extcard" id={ext.name} aria-labelledby={`ext-h-${ext.name}`}>
      <div className="mkt-q-extcard__head">
        <div className="mkt-q-extcard__title">
          <h2 className="mkt-q-section-title" id={`ext-h-${ext.name}`}>
            {ext.label}
          </h2>
          <span className="mkt-q-muted">{t("queue.ext.version", { v: ext.version })}</span>
          <span className={`mkt-q-status--${ext.configured ? "ok" : "off"}`}>
            <span aria-hidden="true">{ext.configured ? "✓" : "–"}</span>{" "}
            {ext.configured ? t("queue.ext.configured") : t("queue.ext.notConfigured")}
          </span>
        </div>
        <label className="mkt-q-check">
          <input
            type="checkbox"
            role="switch"
            checked={ext.enabled}
            disabled={action.busy === "toggle"}
            onChange={(e) => toggle(e.target.checked)}
          />
          <span>{t("queue.ext.enabled")}</span>
        </label>
      </div>
      <p className="mkt-q-muted">{ext.description}</p>
      {ext.name === "plex" && <PlexSignin onSaved={onSaved} />}
      <form
        className="mkt-q-extform"
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
      >
        {ext.settings_schema.map(field)}
      </form>
      <div className="mkt-q-extcard__actions">
        <Button loading={action.busy === "save"} onClick={save}>
          {t("queue.ext.save")}
        </Button>
        <Button variant="secondary" loading={action.busy === "test"} onClick={test}>
          {t("queue.ext.test")}
        </Button>
        {msg && (
          <span role="status" className={msg.ok ? "mkt-q-status--ok" : "mkt-q-status--bad"}>
            <span aria-hidden="true">{msg.ok ? "✓ " : "✕ "}</span>
            {msg.text}
          </span>
        )}
      </div>
      {lines && ext.name !== "cloudflare_stream" && <RemoteTestLines result={lines} />}
      {ext.name === "cloudflare_stream" && <CloudflareRemote refresh={tested} />}
      {action.error && (
        <p role="alert" className="mkt-q-banner">
          {action.error}
        </p>
      )}
    </section>
  );
}

export function AdminExtensions() {
  const { t } = useI18n();
  const [list, setList] = useState<Extension[] | null>(null);
  const [failed, setFailed] = useState(false);

  const load = useCallback(() => {
    queueApi
      .extensions()
      .then((r) => {
        setList(r.extensions);
        setFailed(false);
      })
      .catch(() => setFailed(true));
  }, []);
  useEffect(load, [load]);

  const replace = (e: Extension) =>
    setList((prev) => (prev ?? []).map((x) => (x.name === e.name ? e : x)));

  return (
    <section className="mkt-page mkt-q">
      <header className="mkt-q-detailhead">
        <div>
          <h1>{t("queue.ext.title")}</h1>
          <p className="mkt-q-muted">{t("queue.ext.intro")}</p>
        </div>
        <Link className="mkt-q-linkbtn" to="/queue">
          {t("queue.ext.back")}
        </Link>
      </header>
      {failed && !list ? (
        <p role="alert" className="mkt-q-banner">
          {t("queue.ext.error")}
        </p>
      ) : !list ? (
        <p className="mkt-loading" role="status">
          {t("common.loading")}
        </p>
      ) : list.length === 0 ? (
        <p className="mkt-q-empty">{t("queue.ext.none")}</p>
      ) : (
        <div className="mkt-q-extlist">
          {list.map((e) => (
            <ExtensionCard key={e.name} ext={e} onSaved={replace} />
          ))}
        </div>
      )}
    </section>
  );
}
