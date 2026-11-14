// Admin → Live TV → Broadcast (Epic 50 story 50.22): publish chosen channels to viewers
// outside the home through Maktaba Cloud. Per channel: its setup (address, access, mode,
// ladder, countries), its state and link; Publish runs the home's six checks and names the
// first that fails. The uplink card measures the upload. While livetv.broadcast is off the
// API answers 409 component_off and the panel offers to turn it on (through the same
// preview as the Components page). The API enforces every rule (admins only, the checks);
// the panel only shows them.
//
// Story 50.23: per channel, how much of its next 7 days is publishable and the sources that
// would add the most, each with Record permission (pre-filled, its About page opened); the
// permissions recorded, each withdrawable; the publishable-only switch (off takes a typed
// confirmation); and each channel's countries as an allow or a deny list.
//
// Story 50.24: the notices Maktaba sent about the published channels, the strikes, and the
// counter-notice form (BroadcastNotices).
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { CopyField } from "../../../components/CopyField";
import { BroadcastNotices } from "./BroadcastNotices";
import { ApiError } from "../../../lib/api";
import { componentsApi, refusalOf } from "../../../lib/componentsApi";
import { useFlag } from "../../../lib/flags";
import { useI18n } from "../../../lib/i18n";
import {
  broadcastRefusal,
  revokedReason,
  tvBroadcastApi,
  watchLink,
  type BroadcastChannel,
  type BroadcastRefusal,
  type BroadcastSetup,
  type BroadcastView,
  type GeoMode,
  type PermissionRequest,
  type PublishableView,
  type Share,
  type Suggestion,
} from "../../../lib/tvBroadcastApi";

export const BROADCAST_COMPONENT = "livetv.broadcast";

const mbps = (kbps: number) => (kbps / 1000).toFixed(1);

type Load =
  | { kind: "loading" }
  | { kind: "hidden" }
  | { kind: "off" }
  | { kind: "ready"; view: BroadcastView; pub: PublishableView | null };

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
  const [recording, setRecording] = useState<Partial<PermissionRequest> | null>(null);
  const alive = useRef(true);

  const refusalText = useCallback(
    (r: BroadcastRefusal) => {
      const params: Record<string, string | number> = { ...r.params };
      if (typeof params.kbps === "number") params.kbps = `${mbps(params.kbps)} Mbps`;
      if (typeof params.reason === "string") params.reason = t(`broadcast.reason.${params.reason}`);
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
      // the publishable view is help, not the page: without it the page still works
      const pub = await tvBroadcastApi.publishable().catch(() => null);
      if (alive.current) setLoad({ kind: "ready", view, pub });
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

  const { view, pub } = load;
  const shareOf = (id: string) => pub?.shares.find((x) => x.channel_id === id);
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
      {view.revocations?.home && (
        <p role="alert" data-testid="broadcast-stopped">
          {t(
            view.revocations.home.action === "terminate"
              ? "broadcast.terminated"
              : "broadcast.revoked.suspended",
            { reason: t(`broadcast.reason.${view.revocations.home.reason}`) }
          )}
        </p>
      )}
      {view.revocations?.stale && (
        <p role="status" data-testid="broadcast-stale">
          {t("broadcast.revocationsStale", { days: view.revocations.days })}
        </p>
      )}

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
                  {c.state === "revoked" ? (
                    <div className="mkt-livetv__hint">
                      {revokedReason(c.detail)
                        ? t("broadcast.revoked.channel", {
                            reason: t(`broadcast.reason.${revokedReason(c.detail)}`),
                          })
                        : t("broadcast.offAir")}
                    </div>
                  ) : (
                    c.detail && <div className="mkt-livetv__hint">{t(`broadcast.${c.detail}`)}</div>
                  )}
                  {published && c.slug && (
                    <CopyField label={t("broadcast.link")} value={watchLink(c.slug)} />
                  )}
                  {published && pub && !pub.publishable_only && (
                    <div className="mkt-livetv__hint" data-testid={`broadcast-notlimited-${c.id}`}>
                      {t("broadcast.notLimited")}
                    </div>
                  )}
                  <ShareCell
                    share={shareOf(c.id)}
                    busy={busy}
                    onRecord={(sg) => {
                      setRecording({ kind: sg.kind, ref: sg.ref, label: sg.label });
                      // the proof is on the source's own page: open it beside the form
                      if (sg.url) window.open(sg.url, "_blank", "noopener,noreferrer");
                    }}
                  />
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

      {pub && (
        <PublishableSection
          pub={pub}
          busy={busy}
          onRecord={() => setRecording({})}
          onError={show}
          onChanged={() => void reload()}
        />
      )}

      <BroadcastNotices />

      {recording && (
        <RecordDialog
          initial={recording}
          onClose={() => setRecording(null)}
          onSaved={() => {
            setRecording(null);
            void reload();
          }}
        />
      )}
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
    geo_mode: c.geo_mode || "allow",
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
      <fieldset>
        <legend>{t("broadcast.geoMode")}</legend>
        {(["allow", "deny"] as GeoMode[]).map((g) => (
          <label key={g}>
            <input
              type="radio"
              name="geo_mode"
              checked={s.geo_mode === g}
              onChange={() => setS({ ...s, geo_mode: g })}
            />{" "}
            {t(`broadcast.geoMode.${g}`)}
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

const hours = (secs: number) => Math.round(secs / 360) / 10;

/** A channel's publishable share of its next 7 days, and the sources that would add most. */
function ShareCell(props: {
  share: Share | undefined;
  busy: boolean;
  onRecord: (s: Suggestion) => void;
}) {
  const { t } = useI18n();
  const sh = props.share;
  if (!sh || sh.scheduled_seconds === 0) return null;
  return (
    <div className="mkt-livetv__hint" data-testid={`broadcast-share-${sh.channel_id}`}>
      <div>{t("broadcast.share", { percent: sh.percent })}</div>
      {/* under a quarter can't fill 6 of 24 hours: publishing would be refused */}
      {sh.percent < 25 && <div>{t("broadcast.notEnough")}</div>}
      {sh.suggestions.length > 0 && (
        <details>
          <summary>{t("broadcast.suggestions")}</summary>
          <ul>
            {sh.suggestions.map((sg) => (
              <li key={`${sg.kind}:${sg.ref}`}>
                {sg.label || sg.ref} · {t(`broadcast.permission.kind.${sg.kind}`)} ·{" "}
                {t("broadcast.suggestion.hours", { hours: hours(sg.seconds) })}{" "}
                <Button
                  variant="secondary"
                  disabled={props.busy}
                  onClick={() => props.onRecord(sg)}
                >
                  {t("broadcast.recordPermission")}
                </Button>
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}

/** The permissions recorded, the publishable-only switch, and what may be broadcast. */
function PublishableSection(props: {
  pub: PublishableView;
  busy: boolean;
  onRecord: () => void;
  onError: (e: unknown) => void;
  onChanged: () => void;
}) {
  const { t } = useI18n();
  const [turningOff, setTurningOff] = useState(false);
  const [confirm, setConfirm] = useState("");
  const [withdrawing, setWithdrawing] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const standing = props.pub.sources.filter((s) => !s.revoked_at);
  const run = async (f: () => Promise<unknown>) => {
    try {
      await f();
      props.onChanged();
    } catch (e) {
      props.onError(e);
    }
  };
  return (
    <section aria-label={t("broadcast.publishable.title")} data-testid="broadcast-publishable">
      <h3>{t("broadcast.publishable.title")}</h3>
      <p className="mkt-livetv__hint">{t("broadcast.publishable.help")}</p>
      <p className="mkt-livetv__hint">{t("broadcast.publishable.notLegalAdvice")}</p>

      <p>
        <strong>{t("broadcast.publishableOnly")}</strong>
        {props.pub.publishable_only ? (
          <Button
            variant="secondary"
            disabled={props.busy}
            onClick={() => {
              setConfirm("");
              setTurningOff(true);
            }}
          >
            {t("broadcast.publishableOnly.turnOff")}
          </Button>
        ) : (
          <Button
            disabled={props.busy}
            onClick={() => void run(() => tvBroadcastApi.setPublishableOnly(true))}
          >
            {t("broadcast.publishableOnly.turnOn")}
          </Button>
        )}
      </p>
      {!props.pub.publishable_only && (
        <p className="mkt-livetv__hint">{t("broadcast.publishableOnly.offHint")}</p>
      )}

      <h4>{t("broadcast.sources")}</h4>
      {standing.length === 0 ? (
        <p className="mkt-livetv__hint">{t("broadcast.sources.none")}</p>
      ) : (
        <ul>
          {standing.map((s) => (
            <li key={s.id} data-testid={`broadcast-source-${s.id}`}>
              {s.label || s.ref} · {t(`broadcast.permission.kind.${s.kind}`)} ·{" "}
              <a href={s.proof_url} target="_blank" rel="noopener noreferrer">
                {t("broadcast.permission.proof")}
              </a>
              {s.non_commercial && (
                <div className="mkt-livetv__hint">{t("broadcast.nonCommercialWarning")}</div>
              )}{" "}
              <Button
                variant="secondary"
                disabled={props.busy}
                onClick={() => {
                  setReason("");
                  setWithdrawing(s.id);
                }}
              >
                {t("broadcast.withdraw")}
              </Button>
            </li>
          ))}
        </ul>
      )}
      <Button variant="secondary" disabled={props.busy} onClick={props.onRecord}>
        {t("broadcast.recordPermission")}
      </Button>

      {turningOff && (
        <Modal open onClose={() => setTurningOff(false)} title={t("broadcast.publishableOnly")}>
          <p>{t("broadcast.publishableOnly.offHint")}</p>
          <Input
            label={t("broadcast.publishableOnly.confirm")}
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
          <div>
            <Button variant="secondary" onClick={() => setTurningOff(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              disabled={!confirm.trim()}
              onClick={() =>
                void run(async () => {
                  await tvBroadcastApi.setPublishableOnly(false, confirm);
                  setTurningOff(false);
                })
              }
            >
              {t("broadcast.publishableOnly.turnOff")}
            </Button>
          </div>
        </Modal>
      )}
      {withdrawing && (
        <Modal open onClose={() => setWithdrawing(null)} title={t("broadcast.withdraw")}>
          <Input
            label={t("broadcast.withdraw.reason")}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
          <div>
            <Button variant="secondary" onClick={() => setWithdrawing(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              onClick={() =>
                void run(async () => {
                  await tvBroadcastApi.withdrawSource(withdrawing, reason);
                  setWithdrawing(null);
                })
              }
            >
              {t("broadcast.withdraw")}
            </Button>
          </div>
        </Modal>
      )}
    </section>
  );
}

/** Record permission: a source that allows sharing, with the link that says so. */
function RecordDialog(props: {
  initial: Partial<PermissionRequest>;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const [r, setR] = useState<PermissionRequest>({
    kind: props.initial.kind ?? "channel",
    ref: props.initial.ref ?? "",
    label: props.initial.label ?? "",
    proof_url: "",
    note: "",
    credit: "",
    non_commercial: false,
  });
  const [error, setError] = useState<string | null>(null);
  const save = async () => {
    setError(null);
    try {
      await tvBroadcastApi.recordSource(r);
      props.onSaved();
    } catch (e) {
      const x = broadcastRefusal(e);
      setError(x ? t(x.code, x.params) : e instanceof Error ? e.message : String(e));
    }
  };
  return (
    <Modal open onClose={props.onClose} title={t("broadcast.recordPermission")}>
      <fieldset>
        <legend>{t("broadcast.permission.kind")}</legend>
        {(["channel", "playlist", "speaker", "label"] as const).map((k) => (
          <label key={k}>
            <input
              type="radio"
              name="source_kind"
              checked={r.kind === k}
              onChange={() => setR({ ...r, kind: k })}
            />{" "}
            {t(`broadcast.permission.kind.${k}`)}
          </label>
        ))}
      </fieldset>
      <Input
        label={t("broadcast.permission.ref")}
        value={r.ref}
        onChange={(e) => setR({ ...r, ref: e.target.value })}
      />
      <Input
        label={t("broadcast.permission.label")}
        value={r.label}
        onChange={(e) => setR({ ...r, label: e.target.value })}
      />
      <Input
        label={t("broadcast.permission.proof")}
        type="url"
        value={r.proof_url}
        onChange={(e) => setR({ ...r, proof_url: e.target.value })}
      />
      <Input
        label={t("broadcast.permission.credit")}
        value={r.credit}
        onChange={(e) => setR({ ...r, credit: e.target.value })}
      />
      <Input
        label={t("broadcast.permission.note")}
        value={r.note}
        onChange={(e) => setR({ ...r, note: e.target.value })}
      />
      <label>
        <input
          type="checkbox"
          checked={r.non_commercial}
          onChange={(e) => setR({ ...r, non_commercial: e.target.checked })}
        />{" "}
        {t("broadcast.permission.nonCommercial")}
      </label>
      {r.non_commercial && <p role="note">{t("broadcast.nonCommercialWarning")}</p>}
      {error && <p role="alert">{error}</p>}
      <div>
        <Button variant="secondary" onClick={props.onClose}>
          {t("common.cancel")}
        </Button>
        <Button onClick={() => void save()} disabled={!r.ref.trim() || !r.proof_url.trim()}>
          {t("broadcast.recordPermission")}
        </Button>
      </div>
    </Modal>
  );
}
