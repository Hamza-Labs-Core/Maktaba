// Epic 46 — big screen's Live TV: the channels with what is on now and how
// far into it (GET /api/channels/now, refreshed every 30 s; the API lists
// only the channels the user may watch), and the live player: the desk one
// (pages/LiveTV/Player, a TV's own keys: ▲/▼ channels, digits, I, G) with its
// links kept under /big.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation } from "react-router-dom";
import { useI18n } from "../../lib/i18n";
import { channelLogo, channelsApi, progressFraction, type NowEntry } from "../../lib/channels";
import { Player, type LiveRoutes } from "../../pages/LiveTV/Player";
import { bigPath, useBigNav } from "../nav";
import { Empty, Failed, Grid, Loading, Picture, Progress, Screen } from "../ui";

const REFRESH_MS = 30_000;

export function ChannelTile({
  entry,
  focusId,
  autoFocus,
}: {
  entry: NowEntry;
  focusId: string;
  autoFocus?: boolean;
}) {
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const c = entry.channel;
  const cur = entry.current ?? null;
  const frac = cur ? progressFraction(cur, Date.now()) : null;
  const logo = channelLogo(c);
  return (
    <button
      type="button"
      role="listitem"
      className="bs-tile bs-tile--channel"
      data-bs-focus={focusId}
      data-bs-autofocus={autoFocus || undefined}
      onClick={() => go(bigPath.channel(c.number))}
    >
      <span className="bs-tile__art">
        <Picture src={cur?.poster_url || logo} />
        <span className="bs-tile__chno">{n(c.number, { useGrouping: false })}</span>
        {frac !== null && <Progress fraction={frac} label={t("big.live.progress")} />}
      </span>
      <bdi className="bs-tile__title">{c.name}</bdi>
      <bdi className="bs-tile__meta">{cur?.title ?? t("guide.noContent")}</bdi>
    </button>
  );
}

export function BigLive() {
  const { t } = useI18n();
  const [entries, setEntries] = useState<NowEntry[] | null>(null);
  const [err, setErr] = useState(false);
  const alive = useRef(true);

  const load = useCallback(() => {
    channelsApi
      .now()
      .then((r) => {
        if (!alive.current) return;
        const items = Array.isArray(r?.items)
          ? r.items.filter((e) => e && e.channel && typeof e.channel.number === "number")
          : [];
        setEntries(items.sort((a, b) => a.channel.number - b.channel.number));
        setErr(false);
      })
      .catch(() => alive.current && setErr(true));
  }, []);

  useEffect(() => {
    alive.current = true;
    load();
    const id = setInterval(load, REFRESH_MS);
    return () => {
      alive.current = false;
      clearInterval(id);
    };
  }, [load]);

  if (err && !entries) return <Failed message={t("common.error")} onRetry={load} />;
  if (!entries) return <Loading />;
  return (
    <Screen title={t("big.tab.live")} testId="big-live">
      {entries.length === 0 ? (
        <Empty>{t("big.live.empty")}</Empty>
      ) : (
        <Grid id="channels">
          {entries.map((e, i) => (
            <ChannelTile
              key={e.channel.id}
              entry={e}
              focusId={`ch:${e.channel.id}`}
              autoFocus={i === 0}
            />
          ))}
        </Grid>
      )}
    </Screen>
  );
}

export function BigLivePlayer() {
  const { state } = useLocation();
  // the player's links stay in big screen, at this entry's depth (Back still works)
  const routes = useMemo<LiveRoutes>(
    () => ({
      channel: (n) => bigPath.channel(n),
      guide: bigPath.live,
      watch: (videoId) => bigPath.watch(videoId),
      state,
    }),
    [state]
  );
  return (
    <div className="bs-live" data-testid="big-live-player">
      <Player routes={routes} />
    </div>
  );
}
