// The channel strip settings (Story 27.11) in the channel editor's Art
// area: whether this channel shows the news-style bar (following the global
// setting, or on / off for this channel), how it looks, its clocks, and a
// live preview with the channel's real now / next lines. Whatever matches
// the global settings (Settings → Channel strip) is not stored on the
// channel, so changing a global default still reaches it.
import { useEffect, useMemo, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Toggle } from "@ds/components/Choice/Toggle";
import { ApiError, apiUrl } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import {
  channelStripApi,
  channelsApi,
  type Channel,
  type ChannelArt,
  type ChannelStrip,
  type StripClock,
  type StripSettings,
} from "../../../lib/channels";
import { rulesApi } from "../../../lib/rulesApi";
import {
  DEFAULT_STRIP,
  effectiveStrip,
  stripColors,
  stripDefaultsFromRules,
} from "../../../lib/strip";
import { StripPreview } from "./StripPreview";
import { StripColorsEditor } from "./StripColors";

type EnabledMode = "follow" | "on" | "off";

/** Numeric settings and their bounds (the API's). */
export const STRIP_RANGES: Partial<Record<keyof StripSettings, [number, number]>> = {
  height_pct: [4, 25],
  bg_opacity: [0, 1],
  font_size_pct: [15, 80],
  scroll_speed: [10, 600],
  show_seconds: [3, 600],
  every_minutes: [1, 240],
  clock_seconds: [2, 120],
  page_seconds: [2, 30],
  rule_height: [0, 20],
};

const COLOR_FIELDS = [
  "bg_color",
  "accent_color",
  "text_color",
  "next_color",
  "clock_text_color",
  "indicator_color",
] as const;

const MAX_CLOCKS = 8;

function validZone(zone: string): boolean {
  try {
    new Intl.DateTimeFormat("en", { timeZone: zone });
    return zone.trim() !== "";
  } catch {
    return false;
  }
}

/** The PATCH body: each setting that differs from the global one, null for the rest. */
export function stripBody(
  global: StripSettings,
  draft: StripSettings,
  mode: EnabledMode
): ChannelStrip {
  const out: Record<string, unknown> = {};
  for (const k of Object.keys(DEFAULT_STRIP) as (keyof StripSettings)[]) {
    if (k === "enabled") continue;
    out[k] = JSON.stringify(global[k]) === JSON.stringify(draft[k]) ? null : draft[k];
  }
  out.enabled = mode === "follow" ? null : mode === "on";
  return out as ChannelStrip;
}

export function StripSection({
  channelId,
  channelName,
  art,
  onChange,
}: {
  channelId: string;
  channelName: string;
  art?: ChannelArt;
  onChange: (c: Channel) => void;
}) {
  const { t } = useI18n();
  const own = art?.strip ?? null;
  const [global, setGlobal] = useState<StripSettings>(DEFAULT_STRIP);
  const [draft, setDraft] = useState<StripSettings>(() => effectiveStrip(DEFAULT_STRIP, own));
  const [mode, setMode] = useState<EnabledMode>(modeOf(own));
  const [touched, setTouched] = useState(false);
  const [lines, setLines] = useState<{
    now: string;
    ref: string | null;
    next: string | null;
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    rulesApi
      .list()
      .then((r) => live && setGlobal(stripDefaultsFromRules(r.sections)))
      .catch(() => {});
    channelsApi
      .now()
      .then((r) => {
        const e = r.items.find((i) => i.channel.id === channelId);
        if (live && e?.current) {
          setLines({
            now: e.current.on_air || e.current.title,
            ref: e.current.strip_ref || null,
            next: e.next ? e.next.on_air || e.next.title : null,
          });
        }
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [channelId]);

  // the server's answer (or the global defaults arriving) resets the form
  useEffect(() => {
    if (touched) return;
    setDraft(effectiveStrip(global, own));
    setMode(modeOf(own));
  }, [global, own, touched]);

  const enabled = mode === "follow" ? global.enabled : mode === "on";
  const errors = useMemo(() => {
    const out: Partial<Record<string, string>> = {};
    for (const [k, [lo, hi]] of Object.entries(STRIP_RANGES) as [string, [number, number]][]) {
      const v = draft[k as keyof StripSettings] as number;
      if (!Number.isFinite(v) || v < lo || v > hi)
        out[k] = t("channels.strip.err.range", { lo, hi });
    }
    for (const k of COLOR_FIELDS) {
      if (!/^#[0-9a-f]{6}$/i.test(draft[k])) out[k] = t("channels.strip.err.color");
    }
    draft.clocks.forEach((c, i) => {
      if (!validZone(c.zone)) out[`clock${i}`] = t("channels.strip.err.zone");
    });
    return out;
  }, [draft, t]);
  const invalid = Object.keys(errors).length > 0;

  function set<K extends keyof StripSettings>(k: K, v: StripSettings[K]) {
    setTouched(true);
    setDraft((d) => ({ ...d, [k]: v }));
  }
  function setClock(i: number, patch: Partial<StripClock>) {
    set(
      "clocks",
      draft.clocks.map((c, j) => (j === i ? { ...c, ...patch } : c))
    );
  }
  const num = (k: keyof StripSettings) => ({
    type: "number" as const,
    value: String(draft[k]),
    error: errors[k],
    onChange: (e: React.ChangeEvent<HTMLInputElement>) =>
      set(k, (e.target.value === "" ? Number.NaN : Number(e.target.value)) as never),
  });

  async function run(call: () => Promise<Channel>) {
    setBusy(true);
    setError(null);
    try {
      const c = await call();
      setTouched(false);
      onChange(c);
    } catch (e) {
      setError(e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"));
    } finally {
      setBusy(false);
    }
  }

  const sampleNow = lines?.now || t("channels.strip.sample.now");
  const sampleNext = lines ? lines.next : t("channels.strip.sample.next");
  const image = art?.loading?.url ? apiUrl(art.loading.url) : null;

  return (
    <div className="mkt-strip" data-testid="strip-section">
      <span className="mkt-art__name">{t("channels.strip.title")}</span>
      <p className="mkt-muted">{t("channels.strip.help")}</p>
      <Select
        label={t("channels.strip.show")}
        value={mode}
        options={[
          {
            value: "follow",
            label: t(global.enabled ? "channels.strip.follow.on" : "channels.strip.follow.off"),
          },
          { value: "on", label: t("channels.strip.on") },
          { value: "off", label: t("channels.strip.off") },
        ]}
        onChange={(e) => {
          setTouched(true);
          setMode(e.target.value as EnabledMode);
        }}
      />
      <StripPreview
        settings={{ ...draft, enabled }}
        now={sampleNow}
        next={sampleNext}
        nowRef={lines ? lines.ref : null}
        channelName={channelName}
        image={image}
      />
      {!enabled && <p className="mkt-muted">{t("channels.strip.offNote")}</p>}
      <div className="mkt-strip__grid">
        <Select
          label={t("channels.strip.position")}
          value={draft.position}
          options={[
            { value: "bottom", label: t("channels.strip.pos.bottom") },
            { value: "top", label: t("channels.strip.pos.top") },
          ]}
          onChange={(e) => set("position", e.target.value as StripSettings["position"])}
        />
        <Select
          label={t("channels.strip.direction")}
          value={draft.direction}
          options={[
            { value: "auto", label: t("channels.strip.dir.auto") },
            { value: "rtl", label: t("channels.strip.dir.rtl") },
            { value: "ltr", label: t("channels.strip.dir.ltr") },
          ]}
          onChange={(e) => set("direction", e.target.value as StripSettings["direction"])}
        />
        <Select
          label={t("channels.strip.overflow")}
          value={draft.overflow}
          options={[
            { value: "cycle", label: t("channels.strip.overflow.cycle") },
            { value: "truncate", label: t("channels.strip.overflow.truncate") },
            { value: "scroll", label: t("channels.strip.overflow.scroll") },
          ]}
          onChange={(e) => set("overflow", e.target.value as StripSettings["overflow"])}
        />
        {draft.overflow === "cycle" && (
          <Input label={t("channels.strip.pageSeconds")} step="1" {...num("page_seconds")} />
        )}
        {draft.overflow === "scroll" && (
          <Input label={t("channels.strip.scrollSpeed")} {...num("scroll_speed")} />
        )}
        <Select
          label={t("channels.strip.when")}
          value={draft.show_mode}
          options={[
            { value: "always", label: t("channels.strip.when.always") },
            { value: "interval", label: t("channels.strip.when.interval") },
          ]}
          onChange={(e) => set("show_mode", e.target.value as StripSettings["show_mode"])}
        />
        {draft.show_mode === "interval" && (
          <>
            <Input label={t("channels.strip.showSeconds")} {...num("show_seconds")} />
            <Input label={t("channels.strip.everyMinutes")} {...num("every_minutes")} />
          </>
        )}
        <Input label={t("channels.strip.height")} step="0.5" {...num("height_pct")} />
        <Input label={t("channels.strip.fontSize")} {...num("font_size_pct")} />
        <Input
          label={t("channels.strip.fillerLabel")}
          value={draft.filler_label}
          placeholder={t("channels.strip.fillerLabel.placeholder")}
          maxLength={60}
          onChange={(e) => set("filler_label", e.target.value)}
        />
      </div>
      <StripColorsEditor
        value={stripColors(draft)}
        defaults={stripColors(global)}
        errors={errors}
        onChange={(c) => {
          setTouched(true);
          setDraft((d) => ({ ...d, ...c }));
        }}
      />
      <Input label={t("channels.strip.ruleHeight")} step="0.5" {...num("rule_height")} />
      {draft.overflow === "cycle" && (
        <Toggle
          checked={draft.page_indicator}
          label={t("channels.strip.pageIndicator")}
          onChange={(e) => set("page_indicator", e.target.checked)}
        />
      )}
      <Toggle
        checked={draft.show_next}
        label={t("channels.strip.showNext")}
        onChange={(e) => set("show_next", e.target.checked)}
      />
      <Toggle
        checked={draft.show_channel_name}
        label={t("channels.strip.showChannelName")}
        onChange={(e) => set("show_channel_name", e.target.checked)}
      />
      <Toggle
        checked={draft.show_clock}
        label={t("channels.strip.showClock")}
        onChange={(e) => set("show_clock", e.target.checked)}
      />
      {draft.show_clock && (
        <div className="mkt-strip__clocks">
          <div className="mkt-strip__grid">
            <Input label={t("channels.strip.clockSeconds")} {...num("clock_seconds")} />
            <Select
              label={t("channels.strip.clockFormat")}
              value={draft.clock_format}
              options={[
                { value: "24h", label: t("channels.strip.clock.24h") },
                { value: "12h", label: t("channels.strip.clock.12h") },
              ]}
              onChange={(e) => set("clock_format", e.target.value as StripSettings["clock_format"])}
            />
            <Select
              label={t("channels.strip.digits")}
              value={draft.digits}
              options={[
                { value: "auto", label: t("channels.strip.digits.auto") },
                { value: "arabic", label: t("channels.strip.digits.arabic") },
                { value: "western", label: t("channels.strip.digits.western") },
              ]}
              onChange={(e) => set("digits", e.target.value as StripSettings["digits"])}
            />
          </div>
          {draft.clocks.map((c, i) => (
            <div className="mkt-strip__clock" key={i} data-testid={`strip-clock-row-${i}`}>
              <Input
                label={t("channels.strip.zone")}
                value={c.zone}
                error={errors[`clock${i}`]}
                onChange={(e) => setClock(i, { zone: e.target.value })}
              />
              <Input
                label={t("channels.strip.labelAr")}
                value={c.label_ar}
                dir="rtl"
                maxLength={30}
                onChange={(e) => setClock(i, { label_ar: e.target.value })}
              />
              <Input
                label={t("channels.strip.labelEn")}
                value={c.label_en}
                maxLength={30}
                onChange={(e) => setClock(i, { label_en: e.target.value })}
              />
              <Button
                size="sm"
                variant="ghost"
                aria-label={t("channels.strip.removeClock", { n: i + 1 })}
                onClick={() =>
                  set(
                    "clocks",
                    draft.clocks.filter((_, j) => j !== i)
                  )
                }
              >
                {t("common.delete")}
              </Button>
            </div>
          ))}
          {draft.clocks.length < MAX_CLOCKS && (
            <div className="mkt-art__actions">
              <Button
                size="sm"
                variant="ghost"
                onClick={() =>
                  set("clocks", [...draft.clocks, { zone: "UTC", label_ar: "", label_en: "" }])
                }
              >
                {t("channels.strip.addClock")}
              </Button>
            </div>
          )}
        </div>
      )}
      <div className="mkt-art__actions">
        <Button
          size="sm"
          disabled={busy || invalid || !touched}
          onClick={() => run(() => channelStripApi.save(channelId, stripBody(global, draft, mode)))}
        >
          {t("channels.strip.save")}
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={busy || (!own && !touched)}
          onClick={() => run(() => channelStripApi.reset(channelId))}
        >
          {t("channels.strip.reset")}
        </Button>
      </div>
      {error && (
        <p className="mkt-form__warn" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

function modeOf(own: ChannelStrip | null | undefined): EnabledMode {
  if (own?.enabled === true) return "on";
  if (own?.enabled === false) return "off";
  return "follow";
}
