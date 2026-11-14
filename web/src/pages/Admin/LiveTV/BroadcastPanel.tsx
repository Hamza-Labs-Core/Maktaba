// Admin → Live TV → Broadcast (Epic 50 story 50.22): publish chosen channels to viewers
// outside the home through Maktaba Cloud. Per channel: its setup (address, access, mode,
// ladder, countries), its state and link; Publish runs the home's six checks and names the
// first that fails. The uplink card measures the upload. While livetv.broadcast is off the
// API answers 409 component_off and the panel offers to turn it on (through the same
// preview as the Components page). The API enforces every rule (admins only, the checks);
// the panel only shows them.
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { CopyField } from "../../../components/CopyField";
import { ApiError } from "../../../lib/api";
import { componentsApi, refusalOf } from "../../../lib/componentsApi";
import { useFlag } from "../../../lib/flags";
import { useI18n } from "../../../lib/i18n";
import {
  broadcastRefusal,
  tvBroadcastApi,
  watchLink,
  type BroadcastChannel,
  type BroadcastRefusal,
  type BroadcastSetup,
  type BroadcastView,
} from "../../../lib/tvBroadcastApi";

export const BROADCAST_COMPONENT = "livetv.broadcast";

const mbps = (kbps: number) => (kbps / 1000).toFixed(1);

type Load =
  | { kind: "loading" }
  | { kind: "hidden" }
  | { kind: "off" }
  | { kind: "ready"; view: BroadcastView };

function isComponentOff(e: unknown): boolean {
  return (
    e instanceof ApiError &&
    e.status === 409 &&
    (e.problem as { code?: string }).code === "component_off"
  );
}

export function BroadcastPanel() {
  const allowed = useFlag("broadcast");
  const { t } = useI18n();
  const [load, setLoad] = useState<Load>({ kind: "loading" });
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<BroadcastChannel | null>(null);
  const [publishing, setPublishing] = useState<BroadcastChannel | null>(null);
  const [refusal, setRefusal] = useState<BroadcastRefusal | null>(null);
  const alive = useRef(true);

  const refusalText = useCallback(
    (r: BroadcastRefusal) => {
      const params: Record<string, string | number> = { ...r.params };
      if (typeof params.kbps === "number") params.kbps = `${mbps(params.kbps)} Mbps`;
      const text = t(r.code, params);
      return r.check ? `${t(`broadcast.check.${r.check}`)}: ${text}` : text;
    },
    [t]
  );

  const show = useCallback(
    (e: unknown) => {
      const r = broadcastRefusal(e);
      if (r) setError(refusalText(r));
      else setError(e instanceof Error ? e.message : String(e));
    },
    [refusalText]
  );

  const reload = useCallback(async () => {
    try {
      const view = await tvBroadcastApi.view();
      if (alive.current) setLoad({ kind: "ready", view });
    } catch (e) {
      if (!alive.current) return;
      if (isComponentOff(e)) setLoad({ kind: "off" });
      else if (e instanceof ApiError && (e.status === 403 || e.status === 401))
        setLoad({ kind: "hidden" });
      else {
        setLoad({ kind: "hidden" });
        show(e);
      }
    }
  }, [show]);

  useEffect(() => {
    alive.current = true;
    if (allowed) void reload();
    return () => {
      alive.current = false;
    };
  }, [allowed, reload]);

  if (!allowed || load.kind === "hidden") {
    return error ? (
      <Card className="mkt-livetv__card">
        <h2>{t("broadcast.title")}</h2>
        <p role="alert">{error}</p>
      </Card>
    ) : null;
  }

  const turnOn = async () => {
    setBusy(true);
    setError(null);
    try {
      const req = { changes: [{ id: BROADCAST_COMPONENT, want: "enabled" as const }] };
      const pre = await componentsApi.preview(req);
      if (pre.refused.length > 0) {
        setError(pre.refused[0].detail || pre.refused[0].code);
        return;
      }
      await componentsApi.apply(req);
      await reload();
    } catch (e) {
      const r = refusalOf(e);
      setError(r ? r.detail || r.code : e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  if (load.kind === "loading") return null;
  if (load.kind === "off") {
    return (
      <Card className="mkt-livetv__card" data-testid="broadcast-off">
        <h2>{t("broadcast.title")}</h2>
        <p className="mkt-livetv__hint">{t("broadcast.enable.body")}</p>
        <Button onClick={() => void turnOn()} disabled={busy}>
          {t("broadcast.enable.button")}
        </Button>
        {error && <p role="alert">{error}</p>}
      </Card>
    );
  }

  const { view } = load;
  const measure = async () => {
    setBusy(true);
    setError(null);
    try {
      await tvBroadcastApi.speedtest();
      await reload();
    } catch (e) {
      show(e);
    } finally {
      setBusy(false);
    }
  };
  const unpublish = async (c: BroadcastChannel) => {
    setBusy(true);
    setError(null);
    try {
      await tvBroadcastApi.unpublish(c.id);
      await reload();
    } catch (e) {
      show(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="mkt-livetv__card" data-testid="broadcast-panel">
      <h2>{t("broadcast.title")}</h2>
      <p className="mkt-livetv__hint">{t("broadcast.intro")}</p>
      <p className="mkt-livetv__hint">{t("broadcast.alwaysOnNote")}</p>
      {view.cloud !== "ok" && <p role="status">{t(`broadcast.cloud.${view.cloud}`)}</p>}
      {view.plan && !view.plan.plan && <p role="status">{t("broadcast.needsPro")}</p>}

      <section aria-label={t("broadcast.uplink.title")}>
        <h3>{t("broadcast.uplink.title")}</h3>
        <p>
          {view.uplink.kbps > 0
            ? t("broadcast.uplink.measured", {
                kbps: `${mbps(view.uplink.kbps)} Mbps`,
                when: view.uplink.measured_at
                  ? new Date(view.uplink.measured_at).toLocaleString()
                  : "—",
              })
            : t("broadcast.uplink.none")}
        </p>
        {view.uplink.kbps > 0 && (
          <p className="mkt-livetv__hint">
            {t("broadcast.uplink.budget", {
              used: `${mbps(view.uplink_used_kbps)} Mbps`,
              budget: `${mbps(view.uplink_budget_kbps)} Mbps`,
            })}
          </p>
        )}
        <Button variant="secondary" onClick={() => void measure()} disabled={busy}>
          {busy ? t("broadcast.uplink.measuring") : t("broadcast.uplink.measure")}
        </Button>
      </section>

      {error && <p role="alert">{error}</p>}

      <table className="mkt-livetv__table">
        <thead>
          <tr>
            <th>{t("broadcast.channel")}</th>
            <th>{t("broadcast.access")}</th>
            <th>{t("broadcast.ladder")}</th>
            <th>{t("broadcast.state")}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {view.channels.map((c) => {
            const published = c.state === "live" || c.state === "offline";
            return (
              <tr key={c.id} data-testid={`broadcast-row-${c.id}`}>
                <td>
                  {c.number} {c.name}
                </td>
                <td>{c.configured ? t(`broadcast.access.${c.access}`) : "—"}</td>
                <td>
                  {c.configured
                    ? describeLadder(c.rungs.slice(c.dropped_rungs), view.source_height)
                    : "—"}
                </td>
                <td>
                  {t(`broadcast.state.${c.state}`)}
                  {c.detail && <div className="mkt-livetv__hint">{t(`broadcast.${c.detail}`)}</div>}
                  {published && c.slug && (
                    <CopyField label={t("broadcast.link")} value={watchLink(c.slug)} />
                  )}
                </td>
                <td>
                  {!published && (
                    <Button variant="secondary" onClick={() => setEditing(c)} disabled={busy}>
                      {t("broadcast.setup")}
                    </Button>
                  )}
                  {!published && c.configured && (
                    <Button
                      onClick={() => {
                        setRefusal(null);
                        setPublishing(c);
                      }}
                      disabled={busy}
                    >
                      {t("broadcast.publish")}
                    </Button>
                  )}
                  {published && (
                    <Button variant="secondary" onClick={() => void unpublish(c)} disabled={busy}>
                      {t("broadcast.unpublish")}
                    </Button>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>

      {editing && (
        <SetupDialog
          channel={editing}
          view={view}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            void reload();
          }}
        />
      )}
      {publishing && (
        <PublishDialog
          channel={publishing}
          refusal={refusal ? refusalText(refusal) : null}
          busy={busy}
          onClose={() => setPublishing(null)}
          onPublish={async () => {
            setBusy(true);
            setRefusal(null);
            try {
              await tvBroadcastApi.publish(publishing.id, true);
              setPublishing(null);
              await reload();
            } catch (e) {
              const r = broadcastRefusal(e);
              if (r) setRefusal(r);
              else setRefusal({ code: "broadcast.cloudRefused", params: {} });
            } finally {
              setBusy(false);
            }
          }}
        />
      )}
    </Card>
  );
}

export function describeLadder(rungs: string[], height: number): string {
  return rungs.map((r) => (r === "src" ? `${height}p` : r)).join(" + ");
}

function PublishDialog(props: {
  channel: BroadcastChannel;
  refusal: string | null;
  busy: boolean;
  onClose: () => void;
  onPublish: () => void;
}) {
  const { t } = useI18n();
  const [ticked, setTicked] = useState(false);
  return (
    <Modal open onClose={props.onClose} title={t("broadcast.publish")}>
      <p>{props.channel.name}</p>
      <label>
        <input type="checkbox" checked={ticked} onChange={(e) => setTicked(e.target.checked)} />{" "}
        {t("broadcast.confirmRights")}
      </label>
      {props.refusal && <p role="alert">{props.refusal}</p>}
      <div>
        <Button variant="secondary" onClick={props.onClose}>
          {t("common.cancel")}
        </Button>
        <Button onClick={props.onPublish} disabled={!ticked || props.busy}>
          {t("broadcast.publish")}
        </Button>
      </div>
    </Modal>
  );
}

function SetupDialog(props: {
  channel: BroadcastChannel;
  view: BroadcastView;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const c = props.channel;
  const [s, setS] = useState<BroadcastSetup>({
    slug:
      c.slug ||
      c.name
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "-")
        .replace(/^-+|-+$/g, "") ||
      `channel-${c.number}`,
    access: c.access,
    mode: c.access === "open" ? "always_on" : c.mode,
    rungs: c.rungs,
    countries: c.countries,
  });
  const [countries, setCountries] = useState(c.countries.join(", "));
  const [error, setError] = useState<string | null>(null);
  const toggleRung = (r: string) =>
    setS((x) => {
      const has = x.rungs.includes(r);
      const next = has ? x.rungs.filter((y) => y !== r) : [...x.rungs, r];
      return { ...x, rungs: props.view.rungs.filter((y) => next.includes(y)) };
    });
  const save = async () => {
    setError(null);
    try {
      const list = countries
        .split(",")
        .map((x) => x.trim().toUpperCase())
        .filter(Boolean);
      await tvBroadcastApi.setup(c.id, { ...s, countries: list });
      props.onSaved();
    } catch (e) {
      const r = broadcastRefusal(e);
      setError(r ? t(r.code, r.params) : e instanceof Error ? e.message : String(e));
    }
  };
  return (
    <Modal open onClose={props.onClose} title={`${t("broadcast.setup")}: ${c.name}`}>
      <Input
        label={t("broadcast.slug")}
        value={s.slug}
        onChange={(e) => setS({ ...s, slug: e.target.value })}
      />
      <fieldset>
        <legend>{t("broadcast.access")}</legend>
        {(["invite", "open"] as const).map((a) => (
          <label key={a}>
            <input
              type="radio"
              name="access"
              checked={s.access === a}
              onChange={() => setS({ ...s, access: a, mode: a === "open" ? "always_on" : s.mode })}
            />{" "}
            {t(`broadcast.access.${a}`)}
          </label>
        ))}
      </fieldset>
      <fieldset>
        <legend>{t("broadcast.runMode")}</legend>
        {(["on_demand", "always_on"] as const).map((m) => (
          <label key={m}>
            <input
              type="radio"
              name="mode"
              checked={s.mode === m}
              disabled={s.access === "open" && m === "on_demand"}
              onChange={() => setS({ ...s, mode: m })}
            />{" "}
            {t(`broadcast.runMode.${m}`)}
          </label>
        ))}
      </fieldset>
      <fieldset>
        <legend>{t("broadcast.ladder")}</legend>
        {props.view.rungs.map((r) => (
          <label key={r}>
            <input type="checkbox" checked={s.rungs.includes(r)} onChange={() => toggleRung(r)} />{" "}
            {r === "src" ? `${props.view.source_height}p` : r}
          </label>
        ))}
      </fieldset>
      <Input
        label={t("broadcast.countries")}
        description={t("broadcast.countriesHint")}
        value={countries}
        onChange={(e) => setCountries(e.target.value)}
      />
      {error && <p role="alert">{error}</p>}
      <div>
        <Button variant="secondary" onClick={props.onClose}>
          {t("common.cancel")}
        </Button>
        <Button onClick={() => void save()}>{t("common.save")}</Button>
      </div>
    </Modal>
  );
}
