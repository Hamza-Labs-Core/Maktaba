// Live TV access (admin): turn on the emulated HDHomeRun tuner and hand
// Plex (or Jellyfin / Emby / any IPTV player) the addresses it needs.
//
//   GET   /api/tv/info  { stream_base_url, hdhr_address, xmltv_url, m3u_url, ... }
//   GET   /api/tv/hdhr  { device_id, friendly_name, tuner_count, enabled, max_tuners }
//   PATCH /api/tv/hdhr  { enabled?, friendly_name?, tuner_count? }
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Toggle } from "@ds/components/Choice/Toggle";
import { AdminGate } from "../../components/AdminGate";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { tvApi, type HdhrDevice, type TvInfo } from "../../lib/channels";

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
        {msg && (
          <p
            role={msg.ok ? "status" : "alert"}
            className={msg.ok ? "mkt-q-status--ok" : "mkt-q-status--bad"}
          >
            <span aria-hidden="true">{msg.ok ? "✓ " : "✕ "}</span>
            {msg.text}
          </p>
        )}
      </Card>

      <Card className="mkt-livetv__card">
        <h2>{t("livetv.addresses")}</h2>
        {!info.base_from_config && <p className="mkt-livetv__hint">{t("livetv.baseGuessed")}</p>}
        <CopyRow label={t("livetv.hdhrAddress")} value={info.hdhr_address} />
        <CopyRow label={t("livetv.xmltvUrl")} value={info.xmltv_url} />
        <CopyRow label={t("livetv.m3uUrl")} value={info.m3u_url} />
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

function CopyRow({ label, value }: { label: string; value: string }) {
  const { t } = useI18n();
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setState("copied");
    } catch {
      setState("failed");
    }
  }
  return (
    <div className="mkt-livetv__copy">
      <span className="mkt-livetv__copy-label">{label}</span>
      <code className="mkt-livetv__copy-value">{value}</code>
      <Button size="sm" variant="secondary" onClick={copy} aria-label={t("livetv.copy", { label })}>
        {state === "copied"
          ? t("livetv.copied")
          : state === "failed"
            ? t("livetv.copyFailed")
            : t("livetv.copyBtn")}
      </Button>
    </div>
  );
}
