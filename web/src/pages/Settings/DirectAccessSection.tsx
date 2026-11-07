// Settings → Storage & discovery (admins): direct access from the internet (Epic 50, story
// 50.15). The home opens a port on its router, gets its own HTTPS certificate and is checked
// from outside by Maktaba Cloud; apps then reach it directly, without the relay. Free on every
// plan.
//
//   GET   /api/direct          { enabled, state, port, internal, by, name, check, online, … }
//   POST  /api/direct/check    ask the cloud to check now (202; 503 no tunnel)
//   GET   /api/rules           the direct section's enabled, map_port, manual_port
//   PATCH /api/rules/direct    { set, reset }
//
// The routes exist only on a home linked to Maktaba Cloud whose cloud.direct component is
// on: a 404 hides the card.
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { rulesApi, rulesError } from "../../lib/rulesApi";

export const DIRECT_POLL_MS = 10_000;

export interface DirectCheck {
  result: "ok" | "closed" | "timeout" | "wrong_home" | "cgnat";
  port: number;
  at: string;
}

export interface DirectStatus {
  enabled: boolean;
  state: "on" | "off" | "cgnat" | "mapping_failed" | "";
  port?: number;
  internal?: number;
  listening: boolean;
  by?: string;
  name?: string;
  not_after?: string;
  check?: DirectCheck;
  online: boolean;
}

interface DirectRules {
  enabled: boolean;
  map_port: boolean;
  manual_port: number;
}

/** The sentence for the home's state (the story's words). */
export function directStateKey(
  s: DirectStatus
): { key: string; port?: number; at?: string } | null {
  if (s.state === "cgnat" || s.check?.result === "cgnat") return { key: "direct.state.cgnat" };
  const port = s.check?.port || s.port || s.internal;
  if (s.state === "mapping_failed") return { key: "direct.state.closed", port };
  switch (s.check?.result) {
    case "ok":
      return { key: "direct.state.ok", port, at: s.check.at };
    case "closed":
      return { key: "direct.state.closed", port };
    case "timeout":
      return { key: "direct.state.timeout", port };
    case "wrong_home":
      return { key: "direct.state.wrongHome", port };
  }
  return s.enabled ? { key: "direct.state.checking" } : null;
}

export function DirectAccessSection({ pollMs = DIRECT_POLL_MS }: { pollMs?: number }) {
  const { t, n, formatDate } = useI18n();
  const toast = useToast();
  const [status, setStatus] = useState<DirectStatus | null>(null);
  const [rules, setRules] = useState<DirectRules | null>(null);
  const [absent, setAbsent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [port, setPort] = useState("");

  const load = useCallback(async () => {
    try {
      const [s, r] = await Promise.all([api.get<DirectStatus>("/api/direct"), rulesApi.list()]);
      setStatus(s);
      const sec = r.sections.find((x) => x.key === "direct");
      if (sec) {
        const v = (name: string) => sec.fields.find((f) => f.name === name)?.value;
        const next = {
          enabled: v("enabled") === true,
          map_port: v("map_port") !== false,
          manual_port: Number(v("manual_port") ?? 0),
        };
        setRules(next);
        setPort((p) => (p === "" && next.manual_port > 0 ? String(next.manual_port) : p));
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setAbsent(true);
    }
  }, []);

  useEffect(() => {
    void load();
    const id = setInterval(() => void load(), pollMs);
    return () => clearInterval(id);
  }, [load, pollMs]);

  if (absent) return null;

  async function save(set: Record<string, unknown>) {
    setBusy(true);
    try {
      await rulesApi.patch("direct", set, []);
      await load();
    } catch (e) {
      toast.show({ tone: "error", message: rulesError(e).message });
    } finally {
      setBusy(false);
    }
  }

  async function savePort(e: FormEvent) {
    e.preventDefault();
    const p = Number(port);
    if (!Number.isInteger(p) || p < 1 || p > 65535) {
      toast.show({ tone: "error", message: t("direct.manualPort") });
      return;
    }
    await save({ manual_port: p });
  }

  async function checkNow() {
    setBusy(true);
    try {
      await api.post("/api/direct/check", {});
      toast.show({ tone: "success", message: t("direct.state.checking") });
      await load();
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  }

  const state = status ? directStateKey(status) : null;
  return (
    <section className="mkt-settings__panel" data-testid="direct-access">
      <h2>{t("direct.title")}</h2>
      <Toggle
        checked={rules?.enabled === true}
        disabled={!rules || busy}
        onChange={() => void save({ enabled: !rules?.enabled })}
        label={t("direct.toggle")}
      />
      {rules?.enabled && (
        <>
          {state && (
            <p role="status">
              {t(state.key, {
                port: state.port ? n(state.port) : "",
                time: state.at ? formatDate(state.at) : "",
              })}
            </p>
          )}
          <Toggle
            checked={rules.map_port === false}
            disabled={busy}
            onChange={() => void save({ map_port: !rules.map_port })}
            label={t("direct.manual")}
          />
          {!rules.map_port && (
            <form onSubmit={(e) => void savePort(e)} className="mkt-settings__row">
              <Input
                label={t("direct.manualPort")}
                inputMode="numeric"
                value={port}
                disabled={busy}
                onChange={(e) => setPort(e.currentTarget.value)}
              />
              <Button
                type="submit"
                variant="secondary"
                disabled={busy || port === String(rules.manual_port)}
              >
                {t("common.save")}
              </Button>
            </form>
          )}
          <Button
            type="button"
            variant="secondary"
            disabled={busy || !status?.online}
            onClick={() => void checkNow()}
          >
            {t("direct.checkNow")}
          </Button>
        </>
      )}
    </section>
  );
}
