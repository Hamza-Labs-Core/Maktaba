// Epic 45 — Libraries (`/library`): a card per library the user may see
// (GET /api/libraries/summary): a 2x2 mosaic of recent posters, the name, the
// number of videos and a pin toggle; "All videos" opens the grid over every
// library. The pinned ones lead the page in the user's order, with up / down
// buttons to reorder them (PUT /api/me/pinned-libraries through lib/pins,
// which the sidebar's Pinned group follows).
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { fetchLibrarySummary, type LibraryRef, type LibrarySummary } from "../../lib/browse";
import { MAX_PINS, movePin, togglePin, usePins } from "../../lib/pins";
import { TitleRow } from "../../help/HelpLink";

/** The 2x2 poster mosaic of a library card (blank tiles fill a short one). */
export function PosterMosaic({ ids }: { ids: string[] }) {
  const tiles = [0, 1, 2, 3].map((i) => ids[i] ?? null);
  return (
    <span className="mkt-libcard__mosaic" aria-hidden="true">
      {tiles.map((id, i) => (id ? <MosaicTile key={id} id={id} /> : <span key={`e${i}`} />))}
    </span>
  );
}

function MosaicTile({ id }: { id: string }) {
  const [broken, setBroken] = useState(false);
  if (broken) return <span />;
  return (
    <img
      src={`/api/videos/${encodeURIComponent(id)}/poster`}
      alt=""
      loading="lazy"
      onError={() => setBroken(true)}
    />
  );
}

/** A Pin / Unpin toggle for one library (aria-pressed). */
export function PinButton({ lib, className }: { lib: LibraryRef; className?: string }) {
  const { t } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const pins = usePins(user?.id);
  const [busy, setBusy] = useState(false);
  const pinned = !!pins?.some((p) => p.id === lib.id);
  const full = !pinned && (pins?.length ?? 0) >= MAX_PINS;

  async function onClick() {
    if (!pins) return;
    setBusy(true);
    try {
      await togglePin(lib, pins);
    } catch (e) {
      toast.show({
        tone: "error",
        message: `${t("libraries.pin.error")}: ${
          e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error")
        }`,
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Button
      variant={pinned ? "secondary" : "ghost"}
      size="sm"
      className={className}
      aria-pressed={pinned}
      aria-label={t(pinned ? "libraries.unpinNamed" : "libraries.pinNamed", { name: lib.name })}
      title={full ? t("libraries.pin.full", { max: MAX_PINS }) : undefined}
      disabled={!pins || busy || full}
      onClick={() => void onClick()}
    >
      {pinned ? t("libraries.unpin") : t("libraries.pin")}
    </Button>
  );
}

export function Libraries() {
  const { t, n } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const pins = usePins(user?.id);
  const [items, setItems] = useState<LibrarySummary[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const [moving, setMoving] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    fetchLibrarySummary()
      .then((r) => {
        if (!cancelled) setItems(r);
      })
      .catch((e) => {
        if (!cancelled) setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
      });
    return () => {
      cancelled = true;
    };
  }, [reload, t]);

  async function move(index: number, delta: number) {
    if (!pins) return;
    setMoving(true);
    try {
      await movePin(pins, index, delta);
    } catch (e) {
      toast.show({
        tone: "error",
        message: `${t("libraries.pin.error")}: ${
          e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error")
        }`,
      });
    } finally {
      setMoving(false);
    }
  }

  return (
    <section className="mkt-page mkt-libraries">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("libraries.title")}</h1>
        </TitleRow>
        <Link className="mkt-btn mkt-btn--ghost" to="/videos">
          {t("libraries.allVideos")}
        </Link>
      </header>

      {err && (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={err}
          action={<Button onClick={() => setReload((x) => x + 1)}>{t("common.retry")}</Button>}
        />
      )}

      {pins && pins.length > 0 && (
        <section className="mkt-libraries__pinned" aria-labelledby="mkt-libraries-pinned">
          <h2 id="mkt-libraries-pinned">{t("libraries.pinned")}</h2>
          <p className="mkt-muted">{t("libraries.pinned.hint")}</p>
          <ol className="mkt-pinlist">
            {pins.map((p, i) => (
              <li key={p.id} className="mkt-pinlist__item">
                <Link to={`/library/${encodeURIComponent(p.id)}`} className="mkt-pinlist__name">
                  <bdi>{p.name}</bdi>
                </Link>
                <span className="mkt-pinlist__actions">
                  <button
                    type="button"
                    className="mkt-btn mkt-btn--ghost"
                    aria-label={t("libraries.moveUp", { name: p.name })}
                    disabled={moving || i === 0}
                    onClick={() => void move(i, -1)}
                  >
                    <span aria-hidden="true">↑</span>
                  </button>
                  <button
                    type="button"
                    className="mkt-btn mkt-btn--ghost"
                    aria-label={t("libraries.moveDown", { name: p.name })}
                    disabled={moving || i === pins.length - 1}
                    onClick={() => void move(i, 1)}
                  >
                    <span aria-hidden="true">↓</span>
                  </button>
                  <PinButton lib={p} />
                </span>
              </li>
            ))}
          </ol>
        </section>
      )}

      {!items && !err && (
        <ul className="mkt-libgrid" aria-busy="true">
          {Array.from({ length: 4 }, (_, i) => (
            <li key={i} className="mkt-libcard">
              <Skeleton variant="rect" height={140} />
              <Skeleton variant="text" lines={1} />
            </li>
          ))}
        </ul>
      )}

      {items && items.length === 0 && !err && (
        <EmptyState
          kind="first_run"
          title={t("libraries.empty.title")}
          description={user?.is_admin ? t("libraries.empty.descAdmin") : t("libraries.empty.desc")}
          action={
            user?.is_admin ? (
              <Link className="mkt-btn mkt-btn--primary" to="/admin/libraries">
                {t("home.empty.manage")}
              </Link>
            ) : undefined
          }
        />
      )}

      {items && items.length > 0 && (
        <>
          <h2 className="mkt-libraries__all">{t("libraries.all")}</h2>
          <ul className="mkt-libgrid">
            {items.map((l) => (
              <li key={l.id} className="mkt-libcard">
                <Link to={`/library/${encodeURIComponent(l.id)}`} className="mkt-libcard__link">
                  <PosterMosaic ids={l.poster_video_ids} />
                  <bdi className="mkt-libcard__name">{l.name}</bdi>
                  <span className="mkt-libcard__meta">
                    {t("libraries.videos", { n: n(l.videos ?? 0) })}
                  </span>
                </Link>
                <PinButton lib={{ id: l.id, name: l.name }} className="mkt-libcard__pin" />
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
