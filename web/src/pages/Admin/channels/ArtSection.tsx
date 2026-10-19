// Channel art: logo / corner watermark / loading screen tiles. On a saved
// channel each tile uploads at once (with progress) and can go back to the
// plan's picture; on a new channel the picked files wait and are uploaded
// right after the first save (ChannelEditor).
import { useEffect, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Badge } from "@ds/components/Badge/Badge";
import { ApiError, apiUrl } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import {
  ART_ACCEPT,
  ART_KINDS,
  WATERMARK_POSITIONS,
  artFileProblem,
  channelArtApi,
  type ArtImage,
  type ArtKind,
  type Channel,
  type ChannelArt,
  type WatermarkPosition,
} from "../../../lib/channels";
import { StripSection } from "./StripSection";

export type PendingArt = Partial<Record<ArtKind, File>>;

/** Art picked before the channel exists: kept until the first save uploads it. */
export function PendingArtSection({
  pending,
  onChange,
}: {
  pending: PendingArt;
  onChange: (p: PendingArt) => void;
}) {
  const { t } = useI18n();
  return (
    <fieldset className="mkt-art">
      <legend>{t("channels.art.title")}</legend>
      <p className="mkt-muted">{t("channels.art.afterSave")}</p>
      <div className="mkt-art__tiles">
        {ART_KINDS.map((kind) => (
          <PendingTile
            key={kind}
            kind={kind}
            file={pending[kind]}
            onPick={(f) => onChange({ ...pending, [kind]: f })}
            onClear={() => {
              const next = { ...pending };
              delete next[kind];
              onChange(next);
            }}
          />
        ))}
      </div>
    </fieldset>
  );
}

function PendingTile({
  kind,
  file,
  onPick,
  onClear,
}: {
  kind: ArtKind;
  file?: File;
  onPick: (f: File) => void;
  onClear: () => void;
}) {
  const { t } = useI18n();
  const input = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [url, setUrl] = useState<string | null>(null);
  const label = t(`channels.art.kind.${kind}`);

  useEffect(() => {
    if (!file || typeof URL.createObjectURL !== "function") {
      setUrl(null);
      return;
    }
    const u = URL.createObjectURL(file);
    setUrl(u);
    return () => URL.revokeObjectURL(u);
  }, [file]);

  function take(f: File) {
    const problem = artFileProblem(kind, f);
    if (problem) {
      setError(t(`channels.art.err.${problem}`));
      return;
    }
    setError(null);
    onPick(f);
  }

  return (
    <div
      className="mkt-art__tile"
      data-testid={`art-${kind}`}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault();
        const f = e.dataTransfer.files?.[0];
        if (f) take(f);
      }}
    >
      <strong className="mkt-art__name">{label}</strong>
      <div className="mkt-art__preview">
        {file && url ? (
          <img src={url} alt={label} />
        ) : file ? (
          <span>{file.name}</span>
        ) : (
          <span className="mkt-muted">{t("channels.art.none")}</span>
        )}
      </div>
      {file && <Badge tone="accent">{t("channels.art.pending")}</Badge>}
      <input
        ref={input}
        type="file"
        hidden
        accept={ART_ACCEPT[kind]}
        aria-label={t("channels.art.chooseFor", { name: label })}
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) take(f);
          if (input.current) input.current.value = "";
        }}
      />
      <div className="mkt-art__actions">
        <Button size="sm" onClick={() => input.current?.click()}>
          {file ? t("channels.art.replace") : t("channels.art.choose")}
        </Button>
        {file && (
          <Button size="sm" variant="ghost" onClick={onClear}>
            {t("common.delete")}
          </Button>
        )}
      </div>
      {error && (
        <p className="mkt-form__warn" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

// ─── Channel art ──────────────────────────────────────────────────────
// Three tiles — logo, corner watermark, loading screen — each showing the
// current picture (an animated GIF plays), taking a drop or a chosen file,
// and going back to the lineup plan's picture on "remove".
export function ArtSection({
  channelId,
  channelName = "",
  art,
  onChange,
}: {
  channelId: string;
  channelName?: string;
  art?: ChannelArt;
  onChange: (c: Channel) => void;
}) {
  const { t } = useI18n();
  return (
    <fieldset className="mkt-art">
      <legend>{t("channels.art.title")}</legend>
      <div className="mkt-art__tiles">
        {ART_KINDS.map((kind) => (
          <ArtTile
            key={kind}
            kind={kind}
            channelId={channelId}
            image={art?.[kind]}
            onChange={onChange}
          />
        ))}
      </div>
      {art && <WatermarkSettings channelId={channelId} art={art} onChange={onChange} />}
      <StripSection channelId={channelId} channelName={channelName} art={art} onChange={onChange} />
    </fieldset>
  );
}

function ArtTile({
  kind,
  channelId,
  image,
  onChange,
}: {
  kind: ArtKind;
  channelId: string;
  image?: ArtImage;
  onChange: (c: Channel) => void;
}) {
  const { t } = useI18n();
  const input = useRef<HTMLInputElement>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [over, setOver] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const label = t(`channels.art.kind.${kind}`);

  async function upload(file: File) {
    const problem = artFileProblem(kind, file);
    if (problem) {
      setError(t(`channels.art.err.${problem}`));
      return;
    }
    setError(null);
    setBusy(true);
    setProgress(0);
    try {
      onChange(await channelArtApi.upload(channelId, kind, file, setProgress));
    } catch (e) {
      setError(e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"));
    } finally {
      setBusy(false);
      setProgress(null);
      if (input.current) input.current.value = "";
    }
  }

  async function revert() {
    setError(null);
    setBusy(true);
    try {
      onChange(await channelArtApi.revert(channelId, kind));
    } catch (e) {
      setError(e instanceof ApiError ? e.problem.title : t("common.error"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className={`mkt-art__tile${over ? " mkt-art__tile--over" : ""}`}
      data-testid={`art-${kind}`}
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        const f = e.dataTransfer.files?.[0];
        if (f && !busy) void upload(f);
      }}
    >
      <strong className="mkt-art__name">{label}</strong>
      <div className="mkt-art__preview">
        {image?.url ? (
          <img src={apiUrl(image.url)} alt={label} />
        ) : (
          <span className="mkt-muted">{t("channels.art.none")}</span>
        )}
      </div>
      {image?.url && (
        <Badge tone="neutral">
          {image.overridden ? t("channels.art.custom") : t("channels.art.fromPlan")}
        </Badge>
      )}
      {progress !== null ? (
        <progress
          className="mkt-art__progress"
          max={1}
          value={progress}
          aria-label={t("channels.art.uploading", { pct: Math.round(progress * 100) })}
        />
      ) : (
        <p className="mkt-muted mkt-art__drop">{t("channels.art.drop")}</p>
      )}
      <input
        ref={input}
        type="file"
        hidden
        accept={ART_ACCEPT[kind]}
        aria-label={t("channels.art.chooseFor", { name: label })}
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) void upload(f);
        }}
      />
      <div className="mkt-art__actions">
        <Button size="sm" disabled={busy} onClick={() => input.current?.click()}>
          {image?.url ? t("channels.art.replace") : t("channels.art.choose")}
        </Button>
        {image?.overridden && (
          <Button size="sm" variant="ghost" disabled={busy} onClick={revert}>
            {t("channels.art.remove")}
          </Button>
        )}
      </div>
      <p className="mkt-muted mkt-art__hint">
        {t(kind === "logo" ? "channels.art.hint.logo" : "channels.art.hint.image")}
      </p>
      {error && (
        <p className="mkt-form__warn" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

function WatermarkSettings({
  channelId,
  art,
  onChange,
}: {
  channelId: string;
  art: ChannelArt;
  onChange: (c: Channel) => void;
}) {
  const { t } = useI18n();
  const wm = art.watermark;
  const [position, setPosition] = useState<WatermarkPosition>(wm.position);
  const [width, setWidth] = useState(String(wm.width_pct));
  const [margin, setMargin] = useState(String(wm.margin_pct));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // A new answer from the server (upload, revert) resets the inputs.
  useEffect(() => {
    setPosition(wm.position);
    setWidth(String(wm.width_pct));
    setMargin(String(wm.margin_pct));
  }, [wm.position, wm.width_pct, wm.margin_pct]);

  const w = Number(width);
  const m = Number(margin);
  const widthError =
    width.trim() === "" || !Number.isFinite(w) || w < 1 || w > 50
      ? t("channels.art.err.width")
      : undefined;
  const marginError =
    margin.trim() === "" || !Number.isFinite(m) || m < 0 || m > 25
      ? t("channels.art.err.margin")
      : undefined;
  const dirty = position !== wm.position || w !== wm.width_pct || m !== wm.margin_pct;

  async function apply() {
    setBusy(true);
    setError(null);
    try {
      onChange(await channelArtApi.watermark(channelId, { position, width_pct: w, margin_pct: m }));
    } catch (e) {
      setError(e instanceof ApiError ? e.problem.title : t("common.error"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mkt-art__watermark">
      <span className="mkt-art__name">{t("channels.art.position")}</span>
      <div role="radiogroup" aria-label={t("channels.art.position")} className="mkt-art__corners">
        {WATERMARK_POSITIONS.map((p) => (
          <button
            key={p}
            type="button"
            role="radio"
            aria-checked={position === p}
            className={`mkt-art__corner${position === p ? " mkt-art__corner--on" : ""}`}
            onClick={() => setPosition(p)}
          >
            {t(`channels.art.pos.${p}`)}
          </button>
        ))}
      </div>
      <Input
        label={t("channels.art.width")}
        type="number"
        value={width}
        error={widthError}
        onChange={(e) => setWidth(e.target.value)}
      />
      <Input
        label={t("channels.art.margin")}
        type="number"
        value={margin}
        error={marginError}
        onChange={(e) => setMargin(e.target.value)}
      />
      <div className="mkt-art__actions">
        <Button
          size="sm"
          onClick={apply}
          disabled={busy || !dirty || Boolean(widthError) || Boolean(marginError)}
        >
          {t("channels.art.apply")}
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
