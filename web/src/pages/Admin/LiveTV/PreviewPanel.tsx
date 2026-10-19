// The Changes page's Preview tab (Epic 33, story 33.6): an admin watches a staged
// change before it goes on the air — the staging slot's HLS through the front door
// (the web port's stream gate wants an admin session, the front door the change's
// preview token), never a viewer, Plex or Cloudflare path. The shared TV player
// (@maktaba/tvplayer) switches channels like a TV (up/down, digits); "Live vs
// staging" plays the live channel next to it, following the same channel. The
// staged guide strip shows what the release schedules from the divergence. VLC can
// open the staged lineup (M3U) and guide (XMLTV). No staging HDHomeRun device.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useTvSwitcher, type ResolveContext, type Stream } from "@maktaba/tvplayer";
import { useI18n } from "../../../lib/i18n";
import {
  tvChangesApi,
  type Preview,
  type PreviewChannel,
  type ScheduleChannelDiff,
  type TvChangeDetail,
} from "../../../lib/tvChanges";
import { Clock } from "./changeBits";

/** The states a change can be previewed in (AC1). */
export const PREVIEW_STATES = new Set(["verifying", "ready", "approved"]);

/** A minute before the token ends a new one is minted. */
const RENEW_BEFORE_MS = 60_000;

export function PreviewPanel({ change }: { change: TvChangeDetail }) {
  const { t } = useI18n();
  const can = PREVIEW_STATES.has(change.state);
  const [pv, setPv] = useState<Preview | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [side, setSide] = useState(false);

  const mint = useCallback(async () => {
    setBusy(true);
    setErr(null);
    try {
      setPv(await tvChangesApi.preview(change.id));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [change.id]);

  // mint when the tab opens on a previewable change; renew before the hour ends
  useEffect(() => {
    if (can && !pv && !busy && !err) void mint();
  }, [can, pv, busy, err, mint]);
  useEffect(() => {
    if (!pv) return;
    const left = new Date(pv.expires_at).getTime() - Date.now() - RENEW_BEFORE_MS;
    // a missing or bad expires_at gives NaN: setTimeout(fn, NaN) fires at once and the
    // panel would re-mint in a loop - wait the floor instead
    const id = setTimeout(() => void mint(), Number.isFinite(left) ? Math.max(left, 5_000) : 5_000);
    return () => clearTimeout(id);
  }, [pv, mint]);

  if (!can) {
    return (
      <p className="mkt-tvx-muted" data-testid="preview-off">
        {t("tvchanges.preview.notNow", { state: t(`tvchanges.state.${change.state}`) })}
      </p>
    );
  }
  return (
    <div className="mkt-tvx-diff mkt-tvx-preview" data-testid="preview">
      <p className="mkt-tvx-hint">{t("tvchanges.preview.intro")}</p>
      <div className="mkt-tvx-status" role="status">
        {err ? (
          <p className="mkt-tvx-error">
            {t("tvchanges.preview.error", { error: err })}{" "}
            <Button variant="secondary" size="sm" onClick={() => void mint()} disabled={busy}>
              {t("tvchanges.preview.retry")}
            </Button>
          </p>
        ) : !pv ? (
          <p className="mkt-tvx-muted">{t("tvchanges.preview.minting")}</p>
        ) : (
          <p className="mkt-tvx-muted">
            {t("tvchanges.preview.expires")} <Clock at={pv.expires_at} />
          </p>
        )}
      </div>
      {pv && (
        <>
          <label className="mkt-tvx-preview__side">
            <input
              type="checkbox"
              checked={side}
              onChange={(e) => setSide(e.target.checked)}
              data-testid="preview-side"
            />{" "}
            {t("tvchanges.preview.sideBySide")}
          </label>
          <PreviewPlayers preview={pv} side={side} changeId={change.id} />
          <p className="mkt-tvx-hint">
            {t("tvchanges.preview.vlc")}{" "}
            <a href={pv.m3u} data-testid="preview-m3u">
              {t("tvchanges.preview.m3u")}
            </a>
            {" · "}
            <a href={pv.xmltv} data-testid="preview-xmltv">
              {t("tvchanges.preview.xmltv")}
            </a>
          </p>
        </>
      )}
    </div>
  );
}

function PreviewPlayers({
  preview,
  side,
  changeId,
}: {
  preview: Preview;
  side: boolean;
  changeId: string;
}) {
  const { t } = useI18n();
  const channels = useMemo(() => preview.channels ?? [], [preview.channels]);
  const [want, setWant] = useState<PreviewChannel | null>(null);
  const first = useMemo(
    () => channels.find((c) => c.changed || c.added) ?? channels[0] ?? null,
    [channels]
  );
  const target = want ?? first;
  // the token changes when it is renewed: the resolvers read the newest URLs
  const byId = useRef(new Map<string, PreviewChannel>());
  byId.current = new Map(channels.map((c) => [c.id, c]));

  const resolveStaging = useCallback(
    async (c: PreviewChannel, ctx: ResolveContext): Promise<Stream> => {
      const url = byId.current.get(c.id)?.url ?? c.url;
      return {
        url: ctx.attempt > 0 ? `${url}&retry=${ctx.attempt}` : url,
        path: "staging",
        release() {},
      };
    },
    []
  );
  const staging = useTvSwitcher<PreviewChannel>({
    channels,
    channel: target,
    resolve: resolveStaging,
    pathKey: "staging",
    onSettled: (c) => setWant(c),
  });
  const playing = staging.state.current ?? target;

  return (
    <div className={"mkt-tvx-preview__grid" + (side ? " is-side" : "")}>
      <figure className="mkt-tvx-preview__pane" onKeyDown={staging.onKeyDown} tabIndex={0}>
        <figcaption>
          <span className="mkt-tvx-preview__badge is-staging">
            {t("tvchanges.preview.staging")}
          </span>{" "}
          {playing && <ChannelLabel c={playing} />}
        </figcaption>
        <div className="mkt-live__stage">
          {[0, 1].map((i) => (
            <video
              key={i}
              ref={staging.videoRef[i]}
              data-testid={i === staging.activeIndex ? "preview-video" : "preview-video-standby"}
              className={
                "mkt-live__video" + (i === staging.activeIndex ? " is-active" : " is-standby")
              }
              playsInline
              muted
              controls={i === staging.activeIndex}
              aria-hidden={i === staging.activeIndex ? undefined : true}
            />
          ))}
          <div className="mkt-live__touch" ref={staging.gestureRef} aria-hidden="true" />
        </div>
        {staging.state.phase === "error" && (
          <p className="mkt-tvx-error" role="alert">
            {t("tvchanges.preview.unavailable")}
          </p>
        )}
      </figure>
      {side && playing && <LivePane channel={playing} />}
      <ol className="mkt-tvx-preview__list" aria-label={t("tvchanges.preview.channels")}>
        {channels.map((c) => (
          <li key={c.id}>
            <button
              type="button"
              className={"mkt-tvx-preview__ch" + (playing?.id === c.id ? " is-current" : "")}
              aria-current={playing?.id === c.id ? "true" : undefined}
              onClick={() => staging.tune(c)}
              data-testid={`preview-ch-${c.number}`}
            >
              <ChannelLabel c={c} />
            </button>
          </li>
        ))}
      </ol>
      {playing && <GuideStrip changeId={changeId} channel={playing} />}
    </div>
  );
}

function ChannelLabel({ c }: { c: PreviewChannel }) {
  const { t, n } = useI18n();
  return (
    <span>
      <span className="mkt-tvx-num">{n(c.number, { useGrouping: false })}</span> {c.name}
      {c.added && <span className="mkt-tvx-tag"> {t("tvchanges.preview.added")}</span>}
      {c.changed && !c.added && (
        <span className="mkt-tvx-tag"> {t("tvchanges.preview.changed")}</span>
      )}
      {c.removed && <span className="mkt-tvx-tag"> {t("tvchanges.preview.removed")}</span>}
    </span>
  );
}

/** The live channel next to the staged one (it follows the staging player). */
function LivePane({ channel }: { channel: PreviewChannel }) {
  const { t } = useI18n();
  const list = useMemo(() => (channel.live_url ? [channel] : []), [channel]);
  const resolve = useCallback(async (c: PreviewChannel, ctx: ResolveContext): Promise<Stream> => {
    const url = c.live_url ?? "";
    return {
      url: ctx.attempt > 0 ? `${url}?retry=${ctx.attempt}` : url,
      path: "lan",
      release() {},
    };
  }, []);
  const live = useTvSwitcher<PreviewChannel>({
    channels: list,
    channel: channel.live_url ? channel : null,
    resolve,
    pathKey: "live",
  });
  return (
    <figure className="mkt-tvx-preview__pane" data-testid="preview-live">
      <figcaption>
        <span className="mkt-tvx-preview__badge is-live">{t("tvchanges.preview.live")}</span>{" "}
        <ChannelLabel c={channel} />
      </figcaption>
      {channel.live_url ? (
        <div className="mkt-live__stage">
          {[0, 1].map((i) => (
            <video
              key={i}
              ref={live.videoRef[i]}
              data-testid={i === live.activeIndex ? "preview-live-video" : "preview-live-standby"}
              className={
                "mkt-live__video" + (i === live.activeIndex ? " is-active" : " is-standby")
              }
              playsInline
              muted
              controls={i === live.activeIndex}
              aria-hidden={i === live.activeIndex ? undefined : true}
            />
          ))}
        </div>
      ) : (
        <p className="mkt-tvx-muted">{t("tvchanges.preview.notOnLive")}</p>
      )}
    </figure>
  );
}

/** What the staged generation airs on the channel from its divergence. */
function GuideStrip({ changeId, channel }: { changeId: string; channel: PreviewChannel }) {
  const { t } = useI18n();
  const [d, setD] = useState<ScheduleChannelDiff | null>(null);
  useEffect(() => {
    let alive = true;
    setD(null);
    tvChangesApi
      .scheduleChannel(changeId, channel.id)
      .then((x) => alive && setD(x))
      .catch(() => alive && setD(null));
    return () => {
      alive = false;
    };
  }, [changeId, channel.id]);
  return (
    <section className="mkt-tvx-preview__guide" data-testid="preview-guide">
      <h4>{t("tvchanges.preview.guide")}</h4>
      {!d ? (
        <p className="mkt-tvx-muted">{t("tvchanges.preview.guideUnchanged")}</p>
      ) : d.unchanged ? (
        <p className="mkt-tvx-muted">{t("tvchanges.preview.guideUnchanged")}</p>
      ) : (
        <ol>
          {(d.after ?? []).slice(0, 6).map((p) => (
            <li key={p.start_at}>
              <Clock at={p.start_at} /> <span dir="auto">{p.title}</span>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
