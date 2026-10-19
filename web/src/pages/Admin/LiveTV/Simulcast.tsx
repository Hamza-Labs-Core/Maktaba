// Admin → Live TV → Simulcast (story 32.13): send a channel on to YouTube,
// Facebook or any RTMP/RTMPS destination through Cloudflare. Destinations
// (name, kind, ingest URL, stream key - stored on the home, shown masked),
// each channel's attachments (always, or on a schedule: weekly windows,
// while a programme or series airs, during live slots), the rights
// confirmation before one is turned on, per-output status with Start now /
// Stop now, and the month's output-hours against the cap.
import { useCallback, useEffect, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { ApiError } from "../../../lib/api";
import { channelsApi, type Channel } from "../../../lib/channels";
import { useI18n } from "../../../lib/i18n";
import {
  simulcastErrCode,
  tvSimulcastApi,
  type AttachmentInput,
  type AttachmentMode,
  type AttachmentView,
  type Destination,
  type DestinationKind,
  type OutputState,
  type ScheduleRule,
  type SimulcastView,
} from "../../../lib/tvSimulcastApi";
import "../../../styles/simulcast.css";

const KINDS: DestinationKind[] = ["youtube", "facebook", "rtmp"];
const STATE_TONE: Record<OutputState, "success" | "warn" | "neutral" | "accent" | "error"> = {
  live: "success",
  starting: "accent",
  scheduled: "neutral",
  stopping: "warn",
  off: "neutral",
  errored: "error",
};

// the server's reasons (English) -> their translated key
const REASONS: Record<string, string> = {
  "no free input: every remote channel is in use": "simulcast.reason.noInput",
  "this month's simulcast hours are used up": "simulcast.reason.cap",
  "this month's remote budget is used up": "simulcast.reason.budget",
  "Cloudflare Stream is not ready (run Test connection)": "simulcast.reason.notReady",
  "simulcast is switched off": "simulcast.reason.off",
  "the broadcast is reconnecting; the output stays on the input": "simulcast.reason.reconnecting",
  "the window closed": "simulcast.reason.windowClosed",
  "stopped by the admin": "simulcast.reason.stopped",
  "the channel is disabled": "simulcast.reason.channelOff",
};

type Msg = { ok: boolean; text: string } | null;

function useErr() {
  const { t } = useI18n();
  return (e: unknown) => {
    const code = simulcastErrCode(e);
    if (code) {
      const key = `simulcast.error.${code}`;
      const s = t(key);
      if (s !== key) {
        // a validation error keeps the server's detail (which field, which rule)
        const detail = e instanceof ApiError ? e.problem.detail : undefined;
        return code.startsWith("invalid_") && detail ? `${s}: ${detail}` : s;
      }
    }
    return e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e);
  };
}

export function SimulcastPanel() {
  const { t, n, formatDate } = useI18n();
  const errText = useErr();
  const [view, setView] = useState<SimulcastView | null>(null);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [msg, setMsg] = useState<Msg>(null);
  const [editing, setEditing] = useState<Channel | null>(null);
  const [pick, setPick] = useState("");
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setView(await tvSimulcastApi.status());
    } catch {
      setView(null);
    }
  }, []);
  useEffect(() => {
    void load();
    channelsApi
      .list()
      .then((r) => setChannels([...r.items].sort((a, b) => a.number - b.number)))
      .catch(() => setChannels([]));
  }, [load]);

  if (!view) return null;
  const u = view.usage;
  const reason = (r?: string) => (r ? (REASONS[r] ? t(REASONS[r]) : r) : "");

  async function act(key: string, f: () => Promise<unknown>, ok: string) {
    setBusy(key);
    setMsg(null);
    try {
      await f();
      setMsg({ ok: true, text: ok });
      await load();
    } catch (e) {
      setMsg({ ok: false, text: errText(e) });
    } finally {
      setBusy(null);
    }
  }

  return (
    <Card className="mkt-livetv__card mkt-simulcast">
      <h2>{t("simulcast.title")}</h2>
      <p className="mkt-livetv__hint">{t("simulcast.intro")}</p>
      {!view.enabled ? (
        <p className="mkt-q-muted">{t("simulcast.off")}</p>
      ) : (
        !view.ready && (
          <p role="alert" className="mkt-q-banner">
            {t("simulcast.notReady", { reason: reason(view.reason) })}
          </p>
        )
      )}
      <p>
        {t("simulcast.usage", {
          hours: n(Math.round(u.hours * 10) / 10),
          cap: n(u.cap_hours),
          usd: u.cost_usd.toFixed(2),
        })}{" "}
        {u.capped ? (
          <span className="mkt-q-status--bad">{t("simulcast.capped")}</span>
        ) : (
          u.percent >= 80 && <span className="mkt-q-status--off">{t("simulcast.near")}</span>
        )}
      </p>
      {view.notice && view.notice.level >= 80 && (
        <p role="alert" className="mkt-q-banner" data-testid="simulcast-cap-notice">
          {t(view.notice.level >= 100 ? "simulcast.capNotice100" : "simulcast.capNotice80", {
            month: view.notice.month,
            hours: n(Math.round((view.notice.hours ?? u.hours) * 10) / 10),
            cap: n(view.notice.cap_hours ?? u.cap_hours),
          })}
        </p>
      )}
      {u.budget_exhausted && (
        <p role="alert" className="mkt-q-status--bad">
          {t("simulcast.budget")}
        </p>
      )}

      <Destinations view={view} busy={busy} act={act} />

      <h3>{t("simulcast.channels")}</h3>
      <div className="mkt-form mkt-simulcast__pick">
        <Select
          label={t("simulcast.channel")}
          value={pick}
          onChange={(e) => setPick(e.target.value)}
          options={[
            { value: "", label: t("simulcast.channelPick") },
            ...channels.map((c) => ({ value: c.id, label: `${c.number} · ${c.name}` })),
          ]}
        />
        <div>
          <Button
            variant="secondary"
            disabled={!pick || view.destinations.length === 0}
            onClick={() => setEditing(channels.find((c) => c.id === pick) ?? null)}
          >
            {t("simulcast.editChannel")}
          </Button>
        </div>
      </div>
      {view.destinations.length === 0 && (
        <p className="mkt-q-muted">{t("simulcast.needDestination")}</p>
      )}

      {view.attachments.length === 0 ? (
        <p className="mkt-q-muted">{t("simulcast.noAttachments")}</p>
      ) : (
        <table className="mkt-q-cfpushes mkt-simulcast__outputs">
          <caption>{t("simulcast.outputs")}</caption>
          <thead>
            <tr>
              <th scope="col">{t("simulcast.channel")}</th>
              <th scope="col">{t("simulcast.destination")}</th>
              <th scope="col">{t("simulcast.mode")}</th>
              <th scope="col">{t("simulcast.state")}</th>
              <th scope="col">{t("simulcast.rights")}</th>
              <th scope="col" />
            </tr>
          </thead>
          <tbody>
            {view.attachments.map((a) => (
              <OutputRow
                key={a.id}
                a={a}
                busy={busy}
                reason={reason}
                formatDate={formatDate}
                onEdit={() => setEditing(channels.find((c) => c.id === a.channel_id) ?? null)}
                onStart={() =>
                  act("start:" + a.id, () => tvSimulcastApi.start(a.id), t("simulcast.started"))
                }
                onStop={() =>
                  act("stop:" + a.id, () => tvSimulcastApi.stop(a.id), t("simulcast.stopped"))
                }
              />
            ))}
          </tbody>
        </table>
      )}
      {msg && (
        <p
          role={msg.ok ? "status" : "alert"}
          className={msg.ok ? "mkt-q-status--ok" : "mkt-q-status--bad"}
        >
          {msg.text}
        </p>
      )}
      {editing && (
        <ChannelEditor
          channel={editing}
          view={view}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            setEditing(null);
            setMsg({ ok: true, text: t("simulcast.saved") });
            await load();
          }}
        />
      )}
    </Card>
  );
}

function OutputRow({
  a,
  busy,
  reason,
  formatDate,
  onEdit,
  onStart,
  onStop,
}: {
  a: AttachmentView;
  busy: string | null;
  reason: (r?: string) => string;
  formatDate: (d: Date | string | number) => string;
  onEdit: () => void;
  onStart: () => void;
  onStop: () => void;
}) {
  const { t } = useI18n();
  const o = a.output;
  const when = (s?: string) => (s ? formatDate(s) : "");
  // an open output (live, starting, waiting for an input, errored) can be stopped
  const running = o.state !== "off" && !(o.state === "scheduled" && !o.detail);
  return (
    <tr>
      <td>
        <bdi>
          {a.channel_number} · {a.channel_name}
        </bdi>
      </td>
      <td>
        <bdi>{a.destination_name}</bdi>{" "}
        <span className="mkt-q-muted">{t(`simulcast.kind.${a.destination_kind}`)}</span>
      </td>
      <td>
        {t(`simulcast.mode.${a.mode}`)}
        {!a.enabled && <span className="mkt-q-muted"> · {t("simulcast.attachmentOff")}</span>}
        {a.manual && (
          <span className="mkt-q-muted">
            {" "}
            · {t(`simulcast.manual.${a.manual}`)}
            {a.manual_until && ` ${t("simulcast.until", { when: when(a.manual_until) })}`}
          </span>
        )}
      </td>
      <td>
        <Badge tone={STATE_TONE[o.state] ?? "neutral"}>{t(`simulcast.state.${o.state}`)}</Badge>
        {o.next_window && (
          <div className="mkt-q-muted">
            {t("simulcast.next", { when: when(o.next_window.start) })}
          </div>
        )}
        {o.state === "live" && o.window_end && (
          <div className="mkt-q-muted">{t("simulcast.until", { when: when(o.window_end) })}</div>
        )}
        {o.detail && <div className="mkt-q-muted">{reason(o.detail)}</div>}
        {o.error && <div className="mkt-q-status--bad">{o.error}</div>}
        {o.input_error && <div className="mkt-q-status--bad">{o.input_error}</div>}
        {o.cf_status && (
          <div className="mkt-q-muted">{t("simulcast.cfStatus", { status: o.cf_status })}</div>
        )}
        {o.platform_note && <div className="mkt-q-muted">{t("simulcast.platformNote")}</div>}
      </td>
      <td className="mkt-q-muted">
        {a.rights_confirmed_at
          ? t("simulcast.rightsAt", { when: when(a.rights_confirmed_at) })
          : "—"}
      </td>
      <td>
        <div className="mkt-livetv__actions">
          {running ? (
            <Button variant="secondary" loading={busy === "stop:" + a.id} onClick={onStop}>
              {t("simulcast.stopNow")}
            </Button>
          ) : (
            <Button
              variant="secondary"
              disabled={!a.enabled}
              loading={busy === "start:" + a.id}
              onClick={onStart}
            >
              {t("simulcast.startNow")}
            </Button>
          )}
          <Button variant="secondary" onClick={onEdit}>
            {t("simulcast.edit")}
          </Button>
        </div>
      </td>
    </tr>
  );
}

function Destinations({
  view,
  busy,
  act,
}: {
  view: SimulcastView;
  busy: string | null;
  act: (key: string, f: () => Promise<unknown>, ok: string) => Promise<void>;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState<{
    id?: string;
    name: string;
    kind: DestinationKind;
    url: string;
    key: string;
  } | null>(null);
  const [testRes, setTestRes] = useState<Record<string, string>>({});

  const open = (d?: Destination) =>
    setForm(
      d
        ? { id: d.id, name: d.name, kind: d.kind, url: d.ingest_url, key: "" }
        : { name: "", kind: "youtube", url: view.default_ingest.youtube ?? "", key: "" }
    );
  const urlOk = (u: string) => /^rtmps?:\/\/[^/\s@]+/i.test(u.trim());

  async function save() {
    if (!form) return;
    const body = {
      name: form.name.trim(),
      ingest_url: form.url.trim(),
      ...(form.key.trim() ? { stream_key: form.key.trim() } : {}),
    };
    await act(
      "dest",
      () =>
        form.id
          ? tvSimulcastApi.updateDestination(form.id, body)
          : tvSimulcastApi.createDestination({ ...body, kind: form.kind }),
      t("simulcast.destSaved")
    );
    setForm(null);
  }

  return (
    <>
      <h3>{t("simulcast.destinations")}</h3>
      {view.destinations.length === 0 ? (
        <p className="mkt-q-muted">{t("simulcast.noDestinations")}</p>
      ) : (
        <table className="mkt-q-cfpushes mkt-simulcast__dests">
          <thead>
            <tr>
              <th scope="col">{t("simulcast.name")}</th>
              <th scope="col">{t("simulcast.kind")}</th>
              <th scope="col">{t("simulcast.ingestUrl")}</th>
              <th scope="col">{t("simulcast.streamKey")}</th>
              <th scope="col" />
            </tr>
          </thead>
          <tbody>
            {view.destinations.map((d) => (
              <tr key={d.id}>
                <td>
                  <bdi>{d.name}</bdi>
                  {d.in_use && <span className="mkt-q-muted"> · {t("simulcast.inUse")}</span>}
                </td>
                <td>{t(`simulcast.kind.${d.kind}`)}</td>
                <td>
                  <code dir="ltr">{d.ingest_url}</code>
                </td>
                <td>
                  <code dir="ltr">
                    {d.key_set ? d.stream_key_masked : t("simulcast.keyMissing")}
                  </code>
                  {testRes[d.id] && <div className="mkt-q-muted">{testRes[d.id]}</div>}
                </td>
                <td>
                  <div className="mkt-livetv__actions">
                    <Button
                      variant="secondary"
                      loading={busy === "test:" + d.id}
                      onClick={() =>
                        void act(
                          "test:" + d.id,
                          async () => {
                            const r = await tvSimulcastApi.testDestination(d.id);
                            setTestRes((m) => ({
                              ...m,
                              [d.id]: r.ok
                                ? t("simulcast.testOk")
                                : t("simulcast.testFail", { detail: r.detail }),
                            }));
                          },
                          t("simulcast.tested")
                        )
                      }
                    >
                      {t("simulcast.test")}
                    </Button>
                    <Button variant="secondary" onClick={() => open(d)}>
                      {t("simulcast.edit")}
                    </Button>
                    <Button
                      variant="secondary"
                      disabled={d.in_use}
                      loading={busy === "del:" + d.id}
                      onClick={() => {
                        if (!window.confirm(t("simulcast.deleteConfirm", { name: d.name }))) return;
                        void act(
                          "del:" + d.id,
                          () => tvSimulcastApi.deleteDestination(d.id),
                          t("simulcast.deleted")
                        );
                      }}
                    >
                      {t("simulcast.delete")}
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div>
        <Button variant="secondary" onClick={() => open()}>
          {t("simulcast.addDestination")}
        </Button>
      </div>
      {form && (
        <Modal
          open
          onClose={() => setForm(null)}
          title={form.id ? t("simulcast.editDestination") : t("simulcast.addDestination")}
          footer={
            <div className="mkt-livetv__actions">
              <Button variant="secondary" onClick={() => setForm(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                disabled={
                  busy === "dest" ||
                  !form.name.trim() ||
                  !urlOk(form.url) ||
                  (!form.id && !form.key.trim())
                }
                onClick={() => void save()}
              >
                {t("common.save")}
              </Button>
            </div>
          }
        >
          <div className="mkt-form">
            <Input
              label={t("simulcast.name")}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
            <Select
              label={t("simulcast.kind")}
              value={form.kind}
              disabled={!!form.id}
              onChange={(e) => {
                const kind = e.target.value as DestinationKind;
                setForm({ ...form, kind, url: view.default_ingest[kind] ?? "" });
              }}
              options={KINDS.map((k) => ({ value: k, label: t(`simulcast.kind.${k}`) }))}
            />
            <Input
              label={t("simulcast.ingestUrl")}
              dir="ltr"
              value={form.url}
              error={form.url && !urlOk(form.url) ? t("simulcast.urlBad") : undefined}
              description={t("simulcast.ingestHint")}
              onChange={(e) => setForm({ ...form, url: e.target.value })}
            />
            <Input
              label={t("simulcast.streamKey")}
              type="password"
              autoComplete="off"
              dir="ltr"
              value={form.key}
              description={form.id ? t("simulcast.streamKeyKeep") : t("simulcast.streamKeyHint")}
              onChange={(e) => setForm({ ...form, key: e.target.value })}
            />
          </div>
        </Modal>
      )}
    </>
  );
}

type Draft = {
  destination_id: string;
  attached: boolean;
  mode: AttachmentMode;
  rules: ScheduleRule[];
  enabled: boolean;
  wasOn: boolean;
};

const DAYS = [0, 1, 2, 3, 4, 5, 6];

function ChannelEditor({
  channel,
  view,
  onClose,
  onSaved,
}: {
  channel: Channel;
  view: SimulcastView;
  onClose: () => void;
  onSaved: () => void | Promise<void>;
}) {
  const { t } = useI18n();
  const errText = useErr();
  const mine = view.attachments.filter((a) => a.channel_id === channel.id);
  const [drafts, setDrafts] = useState<Draft[]>(() =>
    view.destinations.map((d) => {
      const a = mine.find((x) => x.destination_id === d.id);
      return {
        destination_id: d.id,
        attached: !!a,
        mode: a?.mode ?? "always",
        rules: a?.rules ?? [],
        enabled: a?.enabled ?? false,
        wasOn: a?.enabled ?? false,
      };
    })
  );
  const [rights, setRights] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const max = view.settings.max_outputs_per_channel;
  const attachedCount = drafts.filter((d) => d.attached).length;
  const set = (i: number, p: Partial<Draft>) =>
    setDrafts((ds) => ds.map((d, j) => (j === i ? { ...d, ...p } : d)));
  const turningOn = drafts.some((d) => d.attached && d.enabled && !d.wasOn);
  const invalid = drafts.some((d) => d.attached && d.mode === "schedule" && d.rules.length === 0);

  async function save(confirmed: boolean) {
    setBusy(true);
    setErr(null);
    const body: AttachmentInput[] = drafts
      .filter((d) => d.attached)
      .map((d) => ({
        destination_id: d.destination_id,
        mode: d.mode,
        rules: d.mode === "schedule" ? d.rules : [],
        enabled: d.enabled,
        ...(confirmed ? { rights_confirmed: true } : {}),
      }));
    try {
      await tvSimulcastApi.setChannel(channel.id, body);
      await onSaved();
    } catch (e) {
      if (simulcastErrCode(e) === "rights_confirmation_required") {
        setRights(true);
      } else {
        setErr(errText(e));
      }
    } finally {
      setBusy(false);
    }
  }

  if (rights) {
    return (
      <Modal
        open
        onClose={() => setRights(false)}
        title={t("simulcast.rightsTitle")}
        footer={
          <div className="mkt-livetv__actions">
            <Button variant="secondary" onClick={() => setRights(false)}>
              {t("common.cancel")}
            </Button>
            <Button disabled={busy} onClick={() => void save(true)}>
              {t("simulcast.rightsConfirm")}
            </Button>
          </div>
        }
      >
        <p>{t("simulcast.rightsBody", { name: channel.name })}</p>
        <p className="mkt-q-muted">{t("simulcast.rightsAudit")}</p>
        {err && (
          <p role="alert" className="mkt-q-status--bad">
            {err}
          </p>
        )}
      </Modal>
    );
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={t("simulcast.editorTitle", { name: channel.name })}
      footer={
        <div className="mkt-livetv__actions">
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={busy || invalid || attachedCount > max}
            onClick={() => (turningOn ? setRights(true) : void save(false))}
          >
            {t("common.save")}
          </Button>
        </div>
      }
    >
      <p className="mkt-q-muted">{t("simulcast.maxPerChannel", { max })}</p>
      {drafts.map((d, i) => {
        const dest = view.destinations.find((x) => x.id === d.destination_id);
        if (!dest) return null;
        return (
          <fieldset key={d.destination_id} className="mkt-simulcast__att">
            <legend>
              <Checkbox
                label={`${dest.name} (${t(`simulcast.kind.${dest.kind}`)})`}
                checked={d.attached}
                onChange={(e) => set(i, { attached: e.target.checked })}
              />
            </legend>
            {d.attached && (
              <div className="mkt-form">
                <Checkbox
                  label={t("simulcast.attachmentOn")}
                  checked={d.enabled}
                  onChange={(e) => set(i, { enabled: e.target.checked })}
                />
                <Select
                  label={t("simulcast.mode")}
                  value={d.mode}
                  onChange={(e) => set(i, { mode: e.target.value as AttachmentMode })}
                  options={[
                    { value: "always", label: t("simulcast.mode.always") },
                    { value: "schedule", label: t("simulcast.mode.schedule") },
                  ]}
                />
                {d.mode === "always" && <p className="mkt-q-muted">{t("simulcast.alwaysCost")}</p>}
                {d.mode === "schedule" && (
                  <RulesEditor rules={d.rules} onChange={(rules) => set(i, { rules })} />
                )}
              </div>
            )}
          </fieldset>
        );
      })}
      {attachedCount > max && (
        <p role="alert" className="mkt-q-status--bad">
          {t("simulcast.error.too_many_outputs")}
        </p>
      )}
      {err && (
        <p role="alert" className="mkt-q-status--bad">
          {err}
        </p>
      )}
    </Modal>
  );
}

function RulesEditor({
  rules,
  onChange,
}: {
  rules: ScheduleRule[];
  onChange: (r: ScheduleRule[]) => void;
}) {
  const { t } = useI18n();
  const set = (i: number, p: Partial<ScheduleRule>) =>
    onChange(rules.map((r, j) => (j === i ? { ...r, ...p } : r)));
  return (
    <div className="mkt-simulcast__rules">
      {rules.length === 0 && <p className="mkt-q-status--bad">{t("simulcast.needRule")}</p>}
      {rules.map((r, i) => (
        <div key={i} className="mkt-simulcast__rule">
          <Select
            label={t("simulcast.ruleKind")}
            value={r.kind}
            onChange={(e) => {
              const kind = e.target.value as ScheduleRule["kind"];
              onChange(
                rules.map((x, j) =>
                  j === i
                    ? kind === "window"
                      ? { kind, days: [], start: "20:00", end: "22:00" }
                      : { kind }
                    : x
                )
              );
            }}
            options={(["window", "programme", "series", "live"] as const).map((k) => ({
              value: k,
              label: t(`simulcast.rule.${k}`),
            }))}
          />
          {r.kind === "window" && (
            <>
              <div className="mkt-simulcast__days" role="group" aria-label={t("simulcast.days")}>
                {DAYS.map((d) => (
                  <Checkbox
                    key={d}
                    label={t(`simulcast.day.${d}`)}
                    checked={(r.days ?? []).includes(d)}
                    onChange={(e) =>
                      set(i, {
                        days: e.target.checked
                          ? [...(r.days ?? []), d].sort()
                          : (r.days ?? []).filter((x) => x !== d),
                      })
                    }
                  />
                ))}
              </div>
              <Input
                label={t("simulcast.start")}
                type="time"
                value={r.start ?? ""}
                onChange={(e) => set(i, { start: e.target.value })}
              />
              <Input
                label={t("simulcast.end")}
                type="time"
                value={r.end ?? ""}
                description={t("simulcast.endHint")}
                onChange={(e) => set(i, { end: e.target.value })}
              />
            </>
          )}
          {r.kind === "programme" && (
            <Input
              label={t("simulcast.videoId")}
              dir="ltr"
              value={r.video_id ?? ""}
              onChange={(e) => set(i, { video_id: e.target.value })}
            />
          )}
          {r.kind === "series" && (
            <Input
              label={t("simulcast.seriesId")}
              dir="ltr"
              value={r.series_id ?? ""}
              onChange={(e) => set(i, { series_id: e.target.value })}
            />
          )}
          <div>
            <Button variant="secondary" onClick={() => onChange(rules.filter((_, j) => j !== i))}>
              {t("simulcast.removeRule")}
            </Button>
          </div>
        </div>
      ))}
      <div>
        <Button
          variant="secondary"
          onClick={() =>
            onChange([...rules, { kind: "window", days: [], start: "20:00", end: "22:00" }])
          }
        >
          {t("simulcast.addRule")}
        </Button>
      </div>
    </div>
  );
}
