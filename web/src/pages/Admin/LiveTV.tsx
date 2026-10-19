// Live TV access (admin): turn on the emulated HDHomeRun tuner, connect it
// to Plex Live TV, and hand Plex (or Jellyfin / Emby / any IPTV player) the
// addresses it needs.
//
//   GET   /api/tv/info           { stream_base_url, hdhr_address, xmltv_url, m3u_url, ... }
//   GET   /api/tv/hdhr           { device_id, friendly_name, tuner_count, enabled, max_tuners }
//   PATCH /api/tv/hdhr           { enabled?, friendly_name?, tuner_count? }
//   GET   /api/tv/plex           what the plex_livetv stage last saw in Plex
//   POST  /api/tv/plex/refresh   push the channel map + reload the guide now
//   POST  /api/tv/plex/register  add the tuner and a DVR with the XMLTV guide, once
//   GET   /api/rules, PATCH /api/rules/plexlive   the automatic Plex settings
//   GET   /api/tv/cluster        streaming hosts + on air (Epic 32), every 5 s while visible
//   GET   /api/tv/simulcast      simulcast destinations, attachments and outputs (story 32.13)
import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Toggle } from "@ds/components/Choice/Toggle";
import { AdminGate } from "../../components/AdminGate";
import { CopyField } from "../../components/CopyField";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import {
  tvApi,
  type HdhrDevice,
  type PlexLiveStatus,
  type PlexLiveTuner,
  type TvInfo,
} from "../../lib/channels";
import { rulesApi, rulesError, type RuleSection } from "../../lib/rulesApi";
import { OnAir } from "./LiveTV/OnAir";
import { SimulcastPanel } from "./LiveTV/Simulcast";
import { StreamingHosts } from "./LiveTV/StreamingHosts";
import { useTvCluster } from "./LiveTV/useTvCluster";
import "../../styles/tvcluster.css";

export function AdminLiveTV() {
  return (
    <AdminGate>
      <LiveTVInner />
    </AdminGate>
  );
}

const errText = (e: unknown) =>
  e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e);

function LiveTVInner() {
  const { t } = useI18n();
  const [info, setInfo] = useState<TvInfo | null>(null);
  const [device, setDevice] = useState<HdhrDevice | null>(null);
  const [name, setName] = useState("");
  const [tuners, setTuners] = useState("4");
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [saving, setSaving] = useState(false);

  const adopt = useCallback((d: HdhrDevice) => {
    setDevice(d);
    setName(d.friendly_name);
    setTuners(String(d.tuner_count));
  }, []);

  useEffect(() => {
    let alive = true;
    Promise.all([tvApi.info(), tvApi.hdhr()])
      .then(([i, d]) => {
        if (!alive) return;
        setInfo(i);
        adopt(d);
      })
      .catch((e) => alive && setLoadErr(errText(e)));
    return () => {
      alive = false;
    };
  }, [adopt]);

  async function save(body: Parameters<typeof tvApi.updateHdhr>[0], okText: string) {
    setSaving(true);
    setMsg(null);
    try {
      adopt(await tvApi.updateHdhr(body));
      setMsg({ ok: true, text: okText });
    } catch (e) {
      setMsg({ ok: false, text: errText(e) });
    } finally {
      setSaving(false);
    }
  }

  if (loadErr) {
    return (
      <section className="mkt-page">
        <h1>{t("livetv.title")}</h1>
        <p role="alert">{t("livetv.loadError", { error: loadErr })}</p>
      </section>
    );
  }
  if (!info || !device) return <p className="mkt-loading">{t("common.loading")}</p>;

  const max = device.max_tuners || 16;
  const tunerNum = Number(tuners);
  const tunerErr =
    !Number.isInteger(tunerNum) || tunerNum < 1 || tunerNum > max
      ? t("livetv.tunersRange", { max })
      : undefined;
  const nameErr = !name.trim() ? t("livetv.nameRequired") : undefined;
  const dirty = name.trim() !== device.friendly_name || tunerNum !== device.tuner_count;

  return (
    <section className="mkt-page mkt-livetv">
      <header className="mkt-page__header">
        <h1>{t("livetv.title")}</h1>
      </header>
      <p>{t("livetv.intro")}</p>

      <Card className="mkt-livetv__card">
        <h2>{t("livetv.tuner")}</h2>
        <Toggle
          label={device.enabled ? t("livetv.enabled") : t("livetv.disabled")}
          checked={device.enabled}
          disabled={saving}
          onChange={(e) =>
            save(
              { enabled: e.target.checked },
              e.target.checked ? t("livetv.enabledMsg") : t("livetv.disabledMsg")
            )
          }
        />
        <p className="mkt-livetv__hint">{t("livetv.discoveryHint")}</p>
        <div className="mkt-form">
          <Input
            label={t("livetv.friendlyName")}
            value={name}
            error={nameErr}
            onChange={(e) => setName(e.target.value)}
          />
          <Input
            label={t("livetv.tunerCount")}
            type="number"
            min={1}
            max={max}
            value={tuners}
            error={tunerErr}
            description={t("livetv.tunerCountHint")}
            onChange={(e) => setTuners(e.target.value)}
          />
          <div>
            <Button
              disabled={saving || !dirty || !!tunerErr || !!nameErr}
              onClick={() =>
                save({ friendly_name: name.trim(), tuner_count: tunerNum }, t("livetv.saved"))
              }
            >
              {t("common.save")}
            </Button>
          </div>
        </div>
        <p className="mkt-livetv__hint">
          {t("livetv.deviceId")}: <code>{device.device_id}</code>
        </p>
        {msg && <Message ok={msg.ok} text={msg.text} />}
      </Card>

      <ClusterSections />

      <SimulcastPanel />

      <PlexPanel info={info} device={device} />

      <Card className="mkt-livetv__card">
        <h2>{t("livetv.addresses")}</h2>
        <p className="mkt-livetv__hint">{t("livetv.manualIntro")}</p>
        {!info.base_from_config && <p className="mkt-livetv__hint">{t("livetv.baseGuessed")}</p>}
        <CopyField label={t("livetv.hdhrAddress")} value={info.hdhr_address} />
        <CopyField label={t("livetv.xmltvUrl")} value={info.xmltv_url} />
        <CopyField label={t("livetv.m3uUrl")} value={info.m3u_url} />
      </Card>

      <Card className="mkt-livetv__card">
        <h2>{t("livetv.plexTitle")}</h2>
        <ol className="mkt-livetv__steps">
          <li>{t("livetv.plexStep1")}</li>
          <li>{t("livetv.plexStep2")}</li>
          <li>{t("livetv.plexStep3", { address: info.hdhr_address })}</li>
          <li>{t("livetv.plexStep4", { url: info.xmltv_url })}</li>
          <li>{t("livetv.plexStep5")}</li>
        </ol>
        <p className="mkt-livetv__hint">{t("livetv.otherPlayers")}</p>
      </Card>
    </section>
  );
}

function Message({ ok, text }: { ok: boolean; text: string }) {
  return (
    <p role={ok ? "status" : "alert"} className={ok ? "mkt-q-status--ok" : "mkt-q-status--bad"}>
      <span aria-hidden="true">{ok ? "✓ " : "✕ "}</span>
      {text}
    </p>
  );
}

/** Streaming hosts and On air (Epic 32): hidden when the server has no cluster view. */
function ClusterSections() {
  const { t } = useI18n();
  const { view, error, unavailable, reload } = useTvCluster();
  if (unavailable) return null;
  if (!view) {
    return error ? (
      <p className="mkt-livetv__hint">{t("tv.cluster.loadError", { error })}</p>
    ) : null;
  }
  return (
    <>
      {error && <p className="mkt-livetv__hint">{t("tv.cluster.staleView", { error })}</p>}
      <StreamingHosts view={view} onChanged={reload} />
      <OnAir view={view} onChanged={reload} />
    </>
  );
}

const when = (s: string | null | undefined) => (s ? new Date(s).toLocaleString() : null);

/** The automatic Plex settings (rules section "plexlive"). */
const AUTO_FLAGS = ["auto_register", "auto_refresh_guide", "auto_refresh_channels"] as const;

/** How often the panel re-reads the status while a run is queued or running. */
const PLEX_POLL_MS = 3000;
const BUSY = new Set(["pending", "claimed", "running", "resuming"]);

function PlexPanel({ info, device }: { info: TvInfo; device: HdhrDevice }) {
  const { t } = useI18n();
  const [st, setSt] = useState<PlexLiveStatus | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [rules, setRules] = useState<RuleSection | null>(null);
  const [minutes, setMinutes] = useState("60");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [confirm, setConfirm] = useState(false);
  const polls = useRef(0);

  const load = useCallback(async () => {
    try {
      setSt(await tvApi.plex());
      setErr(null);
    } catch (e) {
      setErr(errText(e));
    }
  }, []);

  useEffect(() => {
    load();
    rulesApi
      .list()
      .then((r) => {
        const sec = r.sections.find((s) => s.key === "plexlive") ?? null;
        setRules(sec);
        const m = sec?.fields.find((f) => f.name === "guide_refresh_min_minutes");
        if (m) setMinutes(String(m.value));
      })
      .catch(() => setRules(null));
  }, [load]);

  // while a run is queued or running, read the status again until it is done
  useEffect(() => {
    if (!BUSY.has(st?.job?.state ?? "") || polls.current > 40) return;
    const id = window.setTimeout(() => {
      polls.current += 1;
      load();
    }, PLEX_POLL_MS);
    return () => window.clearTimeout(id);
  }, [st, load]);

  async function act(kind: "refresh" | "register") {
    setBusy(true);
    setMsg(null);
    setConfirm(false);
    try {
      await (kind === "refresh" ? tvApi.plexRefresh() : tvApi.plexRegister());
      setMsg({ ok: true, text: t("livetv.plex.queued") });
      polls.current = 0;
      await load();
    } catch (e) {
      setMsg({ ok: false, text: errText(e) });
    } finally {
      setBusy(false);
    }
  }

  const field = (n: string) => rules?.fields.find((f) => f.name === n);
  async function setRule(key: string, value: unknown) {
    const f = field(key);
    setBusy(true);
    setMsg(null);
    try {
      if (f && JSON.stringify(f.default) === JSON.stringify(value)) {
        await rulesApi.patch("plexlive", {}, [key]);
      } else {
        await rulesApi.patch("plexlive", { [key]: value }, []);
      }
      setRules((r) =>
        r
          ? {
              ...r,
              fields: r.fields.map((x) =>
                x.name === key ? { ...x, value, changed: value !== x.default } : x
              ),
            }
          : r
      );
      setMsg({ ok: true, text: t("livetv.plex.saved") });
    } catch (e) {
      setMsg({ ok: false, text: rulesError(e).message });
    } finally {
      setBusy(false);
    }
  }

  const report = st?.status ?? {};
  const plexReady = !!st?.plex.enabled && !!st?.plex.configured;
  const registered = !!report.maktaba?.registered;
  const dvrs = report.dvrs ?? [];
  const ch = report.channels;
  const minNum = Number(minutes);
  const minErr =
    !Number.isInteger(minNum) || minNum < 1 ? t("livetv.plex.minutesRange") : undefined;
  const minField = field("guide_refresh_min_minutes");
  const tunerUrl = report.target?.tuner_url || info.stream_base_url;
  const xmltvUrl = report.target?.xmltv_url || info.xmltv_url;

  const kindLabel = (k: PlexLiveTuner["kind"]) =>
    k === "maktaba"
      ? t("livetv.plex.kindMaktaba")
      : k === "dizquetv"
        ? t("livetv.plex.kindDizquetv")
        : t("livetv.plex.kindOther");

  return (
    <Card className="mkt-livetv__card">
      <h2>{t("livetv.plex.title")}</h2>
      <p className="mkt-livetv__hint">{t("livetv.plex.intro")}</p>
      {err && <p role="alert">{t("livetv.plex.loadError", { error: err })}</p>}
      {st && !plexReady && (
        <p className="mkt-livetv__hint">
          {t("livetv.plex.notConfigured")}{" "}
          <Link to="/admin/extensions">{t("livetv.plex.openExtensions")}</Link>
        </p>
      )}
      {st && (
        <dl className="mkt-livetv__plex-facts">
          <dt>{t("livetv.plex.server")}</dt>
          <dd>
            {st.plex.url || "—"}
            {" · "}
            {!st.checked_at
              ? t("livetv.plex.notChecked")
              : report.reachable
                ? t("livetv.plex.reachable")
                : t("livetv.plex.unreachable")}
          </dd>
          <dt>{t("livetv.plex.lastCheck")}</dt>
          <dd>{when(st.checked_at) ?? t("livetv.plex.never")}</dd>
          <dt>{t("livetv.plex.maktabaTuner")}</dt>
          <dd>{registered ? t("livetv.plex.registered") : t("livetv.plex.notRegistered")}</dd>
          <dt>{t("livetv.plex.dvrs")}</dt>
          <dd>
            {dvrs.length === 0 ? (
              st.checked_at ? (
                t("livetv.plex.noDvrs")
              ) : (
                "—"
              )
            ) : (
              <ul className="mkt-livetv__dvrs">
                {dvrs.map((d) => (
                  <li key={d.key}>
                    {d.title || t("livetv.plex.dvrUntitled", { key: d.key })}
                    {": "}
                    {d.devices.map((x) => kindLabel(x.kind)).join(", ") || "—"}
                  </li>
                ))}
              </ul>
            )}
          </dd>
          <dt>{t("livetv.plex.channels")}</dt>
          <dd>
            {ch && ch.mapped != null
              ? t("livetv.plex.channelsMapped", { mapped: ch.mapped, lineup: ch.lineup })
              : "—"}
          </dd>
          <dt>{t("livetv.plex.lastGuide")}</dt>
          <dd>{when(st.guide_reloaded_at) ?? t("livetv.plex.never")}</dd>
          <dt>{t("livetv.plex.lastMap")}</dt>
          <dd>{when(st.channelmap_at) ?? t("livetv.plex.never")}</dd>
          {st.job && BUSY.has(st.job.state) && (
            <>
              <dt>{t("livetv.plex.run")}</dt>
              <dd>{t("livetv.plex.running")}</dd>
            </>
          )}
          {st.job?.state === "paused" && st.job.paused_reason && (
            <>
              <dt>{t("livetv.plex.run")}</dt>
              <dd>{t("livetv.plex.waiting", { reason: st.job.paused_reason })}</dd>
            </>
          )}
          {st.last_error && (
            <>
              <dt>{t("livetv.plex.error")}</dt>
              <dd>{st.last_error}</dd>
            </>
          )}
        </dl>
      )}
      {report.dizquetv_present && (
        <p className="mkt-livetv__hint">{t("livetv.plex.dizquetvKept")}</p>
      )}
      {(report.warnings ?? []).map((w) => (
        <p key={w} className="mkt-livetv__hint">
          {w}
        </p>
      ))}

      <div className="mkt-livetv__actions">
        {!registered && (
          <Button disabled={busy || !plexReady || !device.enabled} onClick={() => setConfirm(true)}>
            {t("livetv.plex.add")}
          </Button>
        )}
        <Button variant="secondary" disabled={busy || !plexReady} onClick={() => act("refresh")}>
          {t("livetv.plex.refresh")}
        </Button>
      </div>
      {!device.enabled && !registered && (
        <p className="mkt-livetv__hint">{t("livetv.plex.tunerOffHint")}</p>
      )}
      {msg && <Message ok={msg.ok} text={msg.text} />}

      {rules && (
        <div className="mkt-form">
          <h3>{t("livetv.plex.autoTitle")}</h3>
          {AUTO_FLAGS.map((k) => (
            <Toggle
              key={k}
              label={t(`livetv.plex.${k}`)}
              checked={!!field(k)?.value}
              disabled={busy || !field(k)}
              onChange={(e) => setRule(k, e.target.checked)}
            />
          ))}
          <Input
            label={t("livetv.plex.guideMinutes")}
            type="number"
            min={1}
            value={minutes}
            error={minErr}
            description={t("livetv.plex.guideMinutesHint")}
            onChange={(e) => setMinutes(e.target.value)}
          />
          <div>
            <Button
              size="sm"
              disabled={busy || !!minErr || !minField || String(minField.value) === minutes}
              onClick={() => setRule("guide_refresh_min_minutes", minNum)}
            >
              {t("livetv.plex.saveMinutes")}
            </Button>
          </div>
          <p className="mkt-livetv__hint">{t("livetv.plex.moreSettings")}</p>
        </div>
      )}

      {confirm && (
        <Modal
          open
          onClose={() => setConfirm(false)}
          title={t("livetv.plex.confirmTitle")}
          footer={
            <div className="mkt-livetv__actions">
              <Button variant="secondary" onClick={() => setConfirm(false)}>
                {t("common.cancel")}
              </Button>
              <Button disabled={busy} onClick={() => act("register")}>
                {t("livetv.plex.confirmAdd")}
              </Button>
            </div>
          }
        >
          <p>{t("livetv.plex.confirmIntro")}</p>
          <ul className="mkt-livetv__changes">
            <li>{t("livetv.plex.change1", { address: tunerUrl })}</li>
            <li>{t("livetv.plex.change2", { url: xmltvUrl })}</li>
            <li>{t("livetv.plex.change3")}</li>
          </ul>
          <p>{t("livetv.plex.confirmKeep")}</p>
        </Modal>
      )}
    </Card>
  );
}
