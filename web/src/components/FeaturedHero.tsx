// Epic 45 Phase 3 (story 45.21) — Home's featured hero: the newest series
// (from its first lesson) or the lesson an admin picked. A wide card with the
// lesson's picture, its name, Play and More. When an admin picked the lesson,
// admins also get "Feature the newest series" (the pick itself is made on a
// video's page: Feature on Home).
import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { setFeaturedPick, type Featured } from "../lib/browse";
import { watchPath } from "../lib/playQueue";
import { formatDuration } from "./VideoViews";

export function FeaturedHero({ featured, onChange }: { featured: Featured; onChange: () => void }) {
  const { t, n } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const [broken, setBroken] = useState(false);
  const [busy, setBusy] = useState(false);
  const { video, series } = featured;
  const isSeries = featured.kind === "series" && !!series;
  const title = isSeries
    ? series!.name || t("libraryPage.seriesUnnamed")
    : video.title || video.filename || video.id;

  async function reset() {
    setBusy(true);
    try {
      await setFeaturedPick(null);
      onChange();
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="mkt-hero" aria-labelledby="mkt-hero-title" data-testid="featured-hero">
      {!broken && (
        <img
          className="mkt-hero__art"
          src={`/api/videos/${encodeURIComponent(video.id)}/poster`}
          alt=""
          onError={() => setBroken(true)}
        />
      )}
      <div className="mkt-hero__body">
        <span className="mkt-hero__label">
          {featured.picked ? t("home.hero.picked") : t("home.hero.newSeries")}
        </span>
        <h2 id="mkt-hero-title" className="mkt-hero__title">
          <bdi>{title}</bdi>
        </h2>
        <p className="mkt-hero__meta">
          {isSeries && typeof series!.episodes === "number" && series!.episodes > 0 && (
            <span>{t("home.hero.lessons", { n: n(series!.episodes) })}</span>
          )}
          {!isSeries && series && (
            <Link to={`/series/${encodeURIComponent(series.id)}`}>
              <bdi>{series.name}</bdi>
            </Link>
          )}
          {!isSeries && typeof video.duration_sec === "number" && video.duration_sec > 0 && (
            <span>{formatDuration(video.duration_sec)}</span>
          )}
        </p>
        <div className="mkt-hero__actions">
          <Link className="mkt-btn mkt-btn--primary" to={watchPath(video.id)}>
            ▶ {isSeries ? t("home.hero.playFirst") : t("home.hero.play")}
          </Link>
          <Link
            className="mkt-btn mkt-btn--secondary"
            to={
              isSeries
                ? `/series/${encodeURIComponent(series!.id)}`
                : `/videos/${encodeURIComponent(video.id)}`
            }
          >
            {isSeries ? t("home.hero.openSeries") : t("home.hero.more")}
          </Link>
          {user?.is_admin && featured.picked && (
            <Button variant="ghost" disabled={busy} onClick={() => void reset()}>
              {t("home.hero.reset")}
            </Button>
          )}
        </div>
      </div>
    </section>
  );
}
