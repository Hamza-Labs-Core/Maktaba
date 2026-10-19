// Admin → Live TV → Streaming hosts (story 32.5): the cluster switches
// (tv_cluster.nodes_stream, policy, spill_at), the notices, and one card per
// machine that streams TV — its state and why it is not taking channels, how
// full it is, and the admin's controls (on/off, capacity, weight, address,
// drain, move now).
import { useEffect, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useI18n } from "../../../lib/i18n";
import {
  clusterErrText,
  tvClusterApi,
  type HostState,
  type HostUpdate,
  type StreamingHost,
  type TvClusterSettings,
  type TvClusterView,
} from "../../../lib/tvClusterApi";
import { ConfirmDialog } from "./ConfirmDialog";

type Msg = { ok: boolean; text: string } | null;

const STATE_TONE: Record<HostState, "success" | "error" | "warn" | "neutral"> = {
  online: "success",
  down: "error",
  draining: "warn",
  disabled: "neutral",
  unfit: "warn",
};

function Message({ msg }: { msg: Msg }) {
  if (!msg) return null;
  return (
    <p
      role={msg.ok ? "status" : "alert"}
      className={msg.ok ? "mkt-q-status--ok" : "mkt-q-status--bad"}
    >
      <span aria-hidden="true">{msg.ok ? "✓ " : "✕ "}</span>
      {msg.text}
    </p>
  );
}

export function StreamingHosts({
  view,
  onChanged,
}: {
  view: TvClusterView;
  onChanged: () => void | Promise<void>;
}) {
  const { t } = useI18n();
  const twoHomes = view.notices.filter((x) => x.kind === "two_homes");
  const others = view.notices.filter((x) => x.kind !== "two_homes");
  const homes = view.hosts.filter((h) => h.role === "home").map((h) => h.name);

  return (
    <Card className="mkt-livetv__card mkt-tvc">
      <h2>{t("tv.cluster.hosts.title")}</h2>
      <p className="mkt-livetv__hint">{t("tv.cluster.hosts.intro")}</p>
      {twoHomes.map((n, i) => {
        const names = n.hosts && n.hosts.length >= 2 ? n.hosts : homes;
        return (
          <div key={`two-${i}`} className="mkt-tvc-banner" role="alert">
            <strong>{t("tv.cluster.notice.twoHomesTitle")}</strong>{" "}
            {t("tv.cluster.notice.twoHomes", {
              a: names[0] ?? "?",
              b: names[1] ?? "?",
            })}
          </div>
        );
      })}
      {others.length > 0 && (
        <ul className="mkt-tvc-notices">
          {others.map((n, i) => (
            <li key={`${n.kind}-${i}`}>
              <Badge tone="warn">{t(`tv.cluster.notice.kind.${n.kind}`)}</Badge> {n.message}
            </li>
          ))}
        </ul>
      )}
      {view.settings && <ClusterSettings settings={view.settings} onChanged={onChanged} />}
      {view.hosts.length === 0 ? (
        <p className="mkt-livetv__hint">{t("tv.cluster.hosts.none")}</p>
      ) : (
        <div className="mkt-tvc-cards">
          {view.hosts.map((h) => (
            <HostCard key={h.host_id} host={h} onChanged={onChanged} />
          ))}
        </div>
      )}
    </Card>
  );
}

function ClusterSettings({
  settings: s,
  onChanged,
}: {
  settings: TvClusterSettings;
  onChanged: () => void | Promise<void>;
}) {
  const { t } = useI18n();
  const [spill, setSpill] = useState(String(s.spill_at));
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<Msg>(null);
  useEffect(() => setSpill(String(s.spill_at)), [s.spill_at]);

  async function set(name: "nodes_stream" | "policy" | "spill_at", value: unknown) {
    setBusy(true);
    setMsg(null);
    try {
      await tvClusterApi.setSetting(name, value);
      setMsg({ ok: true, text: t("tv.cluster.saved") });
      await onChanged();
    } catch (e) {
      setMsg({ ok: false, text: clusterErrText(e) });
    } finally {
      setBusy(false);
    }
  }

  const spillNum = Number(spill);
  const spillErr =
    !Number.isInteger(spillNum) || spillNum < 1 || spillNum > 100
      ? t("tv.cluster.settings.spillRange")
      : undefined;

  return (
    <div className="mkt-tvc-settings">
      <h3>{t("tv.cluster.settings.title")}</h3>
      <Toggle
        label={t("tv.cluster.settings.nodesStream")}
        checked={s.nodes_stream}
        disabled={busy}
        onChange={(e) => set("nodes_stream", e.target.checked)}
      />
      <div className="mkt-tvc-row">
        <Select
          label={t("tv.cluster.settings.policy")}
          value={s.policy}
          disabled={busy}
          description={t(`tv.cluster.settings.policyHint.${s.policy}`)}
          options={[
            { value: "home_first", label: t("tv.cluster.settings.policy.home_first") },
            { value: "balanced", label: t("tv.cluster.settings.policy.balanced") },
          ]}
          onChange={(e) => set("policy", e.target.value)}
        />
        <div className="mkt-tvc-inline">
          <Input
            label={t("tv.cluster.settings.spillAt")}
            type="number"
            min={1}
            max={100}
            value={spill}
            error={spillErr}
            description={t("tv.cluster.settings.spillAtHint")}
            onChange={(e) => setSpill(e.target.value)}
          />
          <Button
            size="sm"
            disabled={busy || !!spillErr || spillNum === s.spill_at}
            onClick={() => set("spill_at", spillNum)}
          >
            {t("tv.cluster.settings.saveSpill")}
          </Button>
        </div>
      </div>
      <p className="mkt-livetv__hint">{t("tv.cluster.settings.more")}</p>
      <Message msg={msg} />
    </div>
  );
}

const isHttpUrl = (v: string) => {
  try {
    const u = new URL(v);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
};

function HostCard({
  host,
  onChanged,
}: {
  host: StreamingHost;
  onChanged: () => void | Promise<void>;
}) {
  const { t, n, formatDate } = useI18n();
  const isHome = host.role === "home";
  const [cap, setCap] = useState(
    host.capacity_override == null ? "" : String(host.capacity_override)
  );
  const [weight, setWeight] = useState(String(host.weight));
  const adminUrl = host.stream_base_url_source === "admin" ? host.stream_base_url : "";
  const [url, setUrl] = useState(adminUrl);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<Msg>(null);
  const [confirmMove, setConfirmMove] = useState(false);

  // follow the server when the admin is not editing a field
  useEffect(
    () => setCap(host.capacity_override == null ? "" : String(host.capacity_override)),
    [host.capacity_override]
  );
  useEffect(() => setWeight(String(host.weight)), [host.weight]);
  useEffect(() => setUrl(adminUrl), [adminUrl]);

  async function put(body: HostUpdate, okText: string) {
    setBusy(true);
    setMsg(null);
    setConfirmMove(false);
    try {
      await tvClusterApi.updateHost(host.host_id, body);
      setMsg({ ok: true, text: okText });
      await onChanged();
    } catch (e) {
      setMsg({ ok: false, text: clusterErrText(e) });
    } finally {
      setBusy(false);
    }
  }

  const capNum = cap.trim() === "" ? null : Number(cap);
  const capErr =
    capNum !== null && (!Number.isInteger(capNum) || capNum < 0 || capNum > 64)
      ? t("tv.cluster.host.capacityRange")
      : undefined;
  const weightNum = Number(weight);
  const weightErr =
    !Number.isInteger(weightNum) || weightNum < 1 || weightNum > 100
      ? t("tv.cluster.host.weightRange")
      : undefined;
  const urlTrim = url.trim();
  const urlErr = urlTrim && !isHttpUrl(urlTrim) ? t("tv.cluster.host.urlInvalid") : undefined;

  const changes: HostUpdate = {};
  if (capNum !== host.capacity_override) changes.capacity_override = capNum;
  if (weightNum !== host.weight) changes.weight = weightNum;
  if (!isHome && urlTrim !== adminUrl) changes.stream_base_url = urlTrim || null;
  const dirty = Object.keys(changes).length > 0;
  const invalid = !!(capErr || weightErr || urlErr);

  // an older or partial API can send these as null: read them as empty
  const load = host.load ?? {};
  const canPush = host.can_push ?? {};
  const encoders = host.encoders ?? [];
  const cpu = load.cpu;
  const push = [canPush.rtmps && "RTMPS", canPush.srt && "SRT"].filter(Boolean);

  return (
    <article className="mkt-tvc-host" aria-label={host.name}>
      <header className="mkt-tvc-host__head">
        <h3>{host.name}</h3>
        <Badge tone={isHome ? "accent" : "neutral"}>{t(`tv.cluster.role.${host.role}`)}</Badge>
        <Badge tone={STATE_TONE[host.state] ?? "neutral"} role="status">
          {t(`tv.cluster.state.${host.state}`)}
        </Badge>
      </header>
      {host.state !== "online" && host.reason && (
        <p className="mkt-tvc-why">{t("tv.cluster.host.why", { reason: host.reason })}</p>
      )}
      <dl className="mkt-livetv__plex-facts">
        <dt>{t("tv.cluster.host.used")}</dt>
        <dd>{t("tv.cluster.host.usedOf", { used: n(host.used), capacity: n(host.capacity) })}</dd>
        <dt>{t("tv.cluster.host.encoders")}</dt>
        <dd>{encoders.length ? encoders.join(", ") : "—"}</dd>
        <dt>{t("tv.cluster.host.cpu")}</dt>
        <dd>
          {cpu == null ? "—" : `${n(Math.round(cpu * 100))}%`}
          {load.gpu_sessions != null &&
            ` · ${t("tv.cluster.host.gpu", {
              used: n(load.gpu_sessions),
              max: load.nvenc_max != null ? n(load.nvenc_max) : "?",
            })}`}
        </dd>
        <dt>{t("tv.cluster.host.lastBeat")}</dt>
        <dd>{host.last_beat_at ? formatDate(host.last_beat_at) : t("tv.cluster.never")}</dd>
        <dt>{t("tv.cluster.host.version")}</dt>
        <dd>{host.version || "—"}</dd>
        <dt>{t("tv.cluster.host.canPush")}</dt>
        <dd>{push.length ? push.join(", ") : t("tv.cluster.no")}</dd>
        <dt>{t("tv.cluster.host.canLive")}</dt>
        <dd>{host.can_live ? t("tv.cluster.yes") : t("tv.cluster.no")}</dd>
        <dt>{t("tv.cluster.host.address")}</dt>
        <dd>
          <bdi className="mkt-mono">{host.stream_base_url || "—"}</bdi>{" "}
          <span className="mkt-livetv__hint">
            ({t(`tv.cluster.host.addressSource.${host.stream_base_url_source}`)})
          </span>
        </dd>
      </dl>

      <Toggle
        label={t("tv.cluster.host.stream")}
        checked={isHome ? true : host.enabled}
        disabled={busy || isHome}
        onChange={(e) =>
          put(
            { enabled: e.target.checked },
            e.target.checked ? t("tv.cluster.host.enabledMsg") : t("tv.cluster.host.disabledMsg")
          )
        }
      />
      {isHome && <p className="mkt-livetv__hint">{t("tv.cluster.host.homeAlways")}</p>}

      <div className="mkt-tvc-row">
        <Input
          label={t("tv.cluster.host.capacity")}
          type="number"
          min={0}
          max={64}
          value={cap}
          placeholder={t("tv.cluster.host.automatic")}
          error={capErr}
          description={t("tv.cluster.host.capacityHint")}
          onChange={(e) => setCap(e.target.value)}
        />
        <Input
          label={t("tv.cluster.host.weight")}
          type="number"
          min={1}
          max={100}
          value={weight}
          error={weightErr}
          description={t("tv.cluster.host.weightHint")}
          onChange={(e) => setWeight(e.target.value)}
        />
      </div>
      {!isHome && (
        <Input
          label={t("tv.cluster.host.baseUrl")}
          type="url"
          dir="ltr"
          value={url}
          placeholder={host.stream_base_url_source === "reported" ? host.stream_base_url : ""}
          error={urlErr}
          description={t("tv.cluster.host.baseUrlHint")}
          onChange={(e) => setUrl(e.target.value)}
        />
      )}
      <div className="mkt-livetv__actions">
        <Button
          size="sm"
          disabled={busy || !dirty || invalid}
          onClick={() => put(changes, t("tv.cluster.saved"))}
        >
          {t("common.save")}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          disabled={busy}
          onClick={() =>
            put(
              { drain: !host.draining },
              host.draining ? t("tv.cluster.host.undrainedMsg") : t("tv.cluster.host.drainedMsg")
            )
          }
        >
          {host.draining ? t("tv.cluster.host.undrain") : t("tv.cluster.host.drain")}
        </Button>
        {host.draining && (
          <Button
            size="sm"
            variant="secondary"
            disabled={busy}
            onClick={() => setConfirmMove(true)}
          >
            {t("tv.cluster.host.moveNow")}
          </Button>
        )}
      </div>
      {host.draining && <p className="mkt-livetv__hint">{t("tv.cluster.host.drainHint")}</p>}
      <Message msg={msg} />

      {confirmMove && (
        <ConfirmDialog
          title={t("tv.cluster.host.moveNowTitle", { name: host.name })}
          confirmLabel={t("tv.cluster.host.moveNow")}
          busy={busy}
          onClose={() => setConfirmMove(false)}
          onConfirm={() => put({ move_now: true }, t("tv.cluster.host.movedMsg"))}
        >
          <p>
            {t("tv.cluster.host.moveNowBody", { count: n(host.channels?.length ?? 0) })}{" "}
            {t("tv.cluster.interrupt")}
          </p>
        </ConfirmDialog>
      )}
    </article>
  );
}
