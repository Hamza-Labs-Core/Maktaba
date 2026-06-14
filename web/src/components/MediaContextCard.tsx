// Story 26.9 — Web context card.
//
// Server contract:
//   GET /api/videos/{id}/context -> {
//     facts?: { rating, runtime_min, genres[], content_rating, cast[],
//               director, summary, summary_lang, attribution[] },
//     related_in_library: [{ video_id, title, reason, via? }],
//     more_like_this:     [{ video_id, title, score? }]
//   }
//
// Pure read path: everything was fetched during the out-of-band enrich
// job, so the card renders inline with the video page (no view-time
// provider calls). Partial cards are first-class — each block renders
// only when its data exists. Un-enriched videos get a minimal card:
// "More like this" + a "Find metadata" CTA into the enrichment flow.
// RTL-correct for Arabic summaries/names via dir="auto".
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Chip } from "@ds/components/Chip/Chip";
import { Badge } from "@ds/components/Badge/Badge";
import { api } from "../lib/api";
import { useI18n } from "../lib/i18n";

interface CastMember {
  entity_id?: string;
  name: string;
  role?: string;
}
interface Attribution {
  provider: string;
  url?: string;
}
interface Facts {
  rating?: Record<string, number> | number;
  runtime_min?: number;
  genres?: string[];
  content_rating?: string;
  cast?: CastMember[];
  director?: string;
  summary?: string;
  summary_lang?: string;
  attribution?: Attribution[];
}
interface Related {
  video_id: string;
  title: string;
  reason: string;
  via?: string;
  score?: number;
}
interface ContextResponse {
  facts?: Facts;
  related_in_library: Related[];
  more_like_this: Related[];
}

const REASON_KEY: Record<string, string> = {
  same_series: "context.reason.sameSeries",
  shared_cast: "context.reason.sharedCast",
  shared_topic: "context.reason.sharedTopic",
  same_collection: "context.reason.sameCollection",
};

function ratingText(rating: Facts["rating"]): string | null {
  if (rating == null) return null;
  if (typeof rating === "number") return rating.toFixed(1);
  const parts = Object.entries(rating).map(([k, v]) => `${k.toUpperCase()} ${Number(v).toFixed(1)}`);
  return parts.length ? parts.join(" · ") : null;
}

export function MediaContextCard({ videoId }: { videoId: string }) {
  const { t, n } = useI18n();
  const [data, setData] = useState<ContextResponse | null>(null);

  useEffect(() => {
    let alive = true;
    api
      .get<ContextResponse>(`/api/videos/${encodeURIComponent(videoId)}/context`)
      .then((r) => {
        if (alive) setData({ ...r, related_in_library: r.related_in_library ?? [], more_like_this: r.more_like_this ?? [] });
      })
      .catch(() => {
        if (alive) setData({ related_in_library: [], more_like_this: [] });
      });
    return () => {
      alive = false;
    };
  }, [videoId]);

  if (!data) return null;

  const facts = data.facts;
  const rating = facts ? ratingText(facts.rating) : null;
  const hasFacts = Boolean(
    facts && (rating || facts.summary || facts.cast?.length || facts.genres?.length || facts.runtime_min)
  );
  const isUnenriched = !hasFacts && data.related_in_library.length === 0;

  return (
    <Card className="mkt-context-card" elevation={1}>
      {hasFacts && facts && (
        <div className="mkt-context-card__facts">
          <div className="mkt-context-card__chips">
            {rating && <Badge tone="accent">★ {rating}</Badge>}
            {facts.runtime_min != null && (
              <Badge tone="neutral">{t("context.runtime", { min: n(facts.runtime_min) })}</Badge>
            )}
            {facts.content_rating && <Badge tone="neutral">{facts.content_rating}</Badge>}
            {(facts.genres ?? []).map((g) => (
              <Chip key={g}>{g}</Chip>
            ))}
          </div>
          {facts.summary && (
            <p className="mkt-context-card__summary" dir="auto" lang={facts.summary_lang}>
              {facts.summary}
            </p>
          )}
          {facts.director && (
            <p className="mkt-context-card__crew">
              <strong>{t("context.director")}:</strong> <span dir="auto">{facts.director}</span>
            </p>
          )}
          {facts.cast && facts.cast.length > 0 && (
            <p className="mkt-context-card__crew">
              <strong>{t("context.cast")}:</strong>{" "}
              <span dir="auto">{facts.cast.map((c) => c.name).join(", ")}</span>
            </p>
          )}
          {facts.attribution && facts.attribution.length > 0 && (
            <p className="mkt-context-card__attr mkt-muted">
              {facts.attribution.map((a) =>
                a.url ? (
                  <a key={a.provider} href={a.url} target="_blank" rel="noopener noreferrer">
                    {t("context.attribution", { provider: a.provider })}
                  </a>
                ) : (
                  <span key={a.provider}>{t("context.attribution", { provider: a.provider })}</span>
                )
              )}
            </p>
          )}
        </div>
      )}

      {data.related_in_library.length > 0 && (
        <RelatedRail title={t("context.related")} items={data.related_in_library} reasonKey={REASON_KEY} t={t} />
      )}

      {data.more_like_this.length > 0 && (
        <RelatedRail title={t("context.moreLikeThis")} items={data.more_like_this} t={t} />
      )}

      {isUnenriched && (
        <div className="mkt-context-card__cta">
          <p className="mkt-muted">{t("context.unenriched")}</p>
          <Link to={`/videos/${videoId}?tab=enrichment`} className="mkt-link-btn">
            {t("context.findMetadata")}
          </Link>
        </div>
      )}
    </Card>
  );
}

function RelatedRail({
  title,
  items,
  reasonKey,
  t,
}: {
  title: string;
  items: Related[];
  reasonKey?: Record<string, string>;
  t: (k: string, v?: Record<string, string | number>) => string;
}) {
  return (
    <div className="mkt-context-rail">
      <h3 className="mkt-context-rail__title">{title}</h3>
      <ul className="mkt-context-rail__items" role="list">
        {items.map((rel) => (
          <li key={`${rel.reason}-${rel.video_id}`}>
            <Link to={`/videos/${rel.video_id}`} className="mkt-context-rail__item">
              <span dir="auto">{rel.title}</span>
              {reasonKey && reasonKey[rel.reason] && (
                <span className="mkt-context-rail__reason mkt-muted">
                  {t(reasonKey[rel.reason])}
                  {rel.via ? ` · ${rel.via}` : ""}
                </span>
              )}
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}
