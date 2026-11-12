// Epic 57 story 57.17: the review list of the age ratings the home detected from
// transcripts.
//
//   GET  /api/parental/review?state=&limit=&offset=   an admin: every library; a
//        guardian: the libraries their children can read; anyone else 403
//   POST /api/parental/review/{video_id} {tier | null}   admin
//
// The list is what nobody has decided yet: a rating the home was not sure of (it
// rates nothing), one it was sure of that no admin confirmed, and a video it could
// not check. An admin confirms the detected rating, gives another, or leaves the
// video without one; a guardian only reads.
import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import {
  REVIEW_PAGE,
  REVIEW_STATES,
  decideReview,
  fetchReview,
  tierKey,
  type ReviewItem,
  type ReviewState,
} from "../../lib/parentalApi";
import { usePageData } from "../../lib/usePageData";
import { LoadError } from "../../components/LoadError";
import { TitleRow } from "../../help/HelpLink";
import { formatDuration } from "../../components/VideoViews";
import { TierOptions, choiceOf, tierOf } from "../../components/parental/AgeTierControl";
import "../../styles/parental.css";

/** Where a video's rating comes from, as the home names it. */
const SOURCES = new Set([
  "admin_video",
  "admin_container",
  "plex",
  "guide",
  "youtube",
  "plugin",
  "detected",
  "library_default",
]);
const TONE = { low_confidence: "warn", unconfirmed: "accent", not_checked: "neutral" } as const;

export function TierReview() {
  const { user } = useAuth();
  const { t, n } = useI18n();
  const toast = useToast();
  const [state, setState] = useState<ReviewState | "">("");
  const [offset, setOffset] = useState(0);
  const { data, err, loading, reload } = usePageData(
    `parental:review:${state}:${offset}`,
    () => fetchReview(state, offset),
    [state, offset]
  );
  const canDecide = user?.is_admin === true;

  return (
    <section className="mkt-page">
      <TitleRow>
        <h1>{t("parental.review.title")}</h1>
      </TitleRow>
      <p className="mkt-parental-note">
        {t("parental.review.intro")}
        {data &&
          ` ${t("parental.review.line", { percent: n(Math.round(data.minConfidence * 100)) })}`}
      </p>
      <label className="mkt-field">
        <span className="mkt-field__label">{t("parental.review.filter")}</span>
        <select
          value={state}
          onChange={(e) => {
            setState(e.target.value as ReviewState | "");
            setOffset(0);
          }}
        >
          <option value="">{t("parental.review.state.all")}</option>
          {REVIEW_STATES.map((s) => (
            <option key={s} value={s}>
              {t(`parental.review.state.${s}`)}
            </option>
          ))}
        </select>
      </label>

      {loading && <p className="mkt-loading">{t("common.loading")}</p>}
      {err != null && !data && <LoadError error={err} onRetry={reload} />}
      {data && data.items.length === 0 && (
        <EmptyState
          kind={state ? "filtered_out" : "cleared"}
          title={t("parental.review.empty.title")}
          description={t("parental.review.empty.desc")}
        />
      )}
      {data && data.items.length > 0 && (
        <>
          <table className="mkt-parental-review">
            <thead>
              <tr>
                <th scope="col">{t("parental.review.col.video")}</th>
                <th scope="col">{t("parental.review.col.detected")}</th>
                <th scope="col">{t("parental.review.col.heard")}</th>
                <th scope="col">{t("parental.review.col.now")}</th>
                {canDecide && <th scope="col">{t("parental.review.col.decide")}</th>}
              </tr>
            </thead>
            <tbody>
              {data.items.map((item) => (
                <Row
                  key={item.videoId}
                  item={item}
                  canDecide={canDecide}
                  onDecided={(msg) => {
                    toast.show({ tone: "success", message: msg });
                    reload();
                  }}
                />
              ))}
            </tbody>
          </table>
          <div className="mkt-parental-rule__row">
            <Button
              variant="secondary"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - REVIEW_PAGE))}
            >
              {t("parental.review.previous")}
            </Button>
            <span>
              {t("parental.review.range", {
                from: n(data.offset + 1),
                to: n(data.offset + data.items.length),
                total: n(data.total),
              })}
            </span>
            <Button
              variant="secondary"
              disabled={data.offset + data.items.length >= data.total}
              onClick={() => setOffset(offset + REVIEW_PAGE)}
            >
              {t("parental.review.next")}
            </Button>
          </div>
        </>
      )}
    </section>
  );
}

function Row({
  item,
  canDecide,
  onDecided,
}: {
  item: ReviewItem;
  canDecide: boolean;
  onDecided: (message: string) => void;
}) {
  const { t, n } = useI18n();
  // starts on what the home detected, so Confirm is one tap
  const [choice, setChoice] = useState(choiceOf(item.detectedTier));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const watch = (at: number) =>
    `/videos/${encodeURIComponent(item.videoId)}/watch?t=${Math.max(0, Math.floor(at))}`;

  async function decide() {
    setBusy(true);
    setError(null);
    try {
      const a = await decideReview(item.videoId, tierOf(choice));
      onDecided(t("parental.tier.nowVideo", { tier: t(tierKey(a.tier)) }));
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 403
          ? t("parental.tier.error.forbidden")
          : e instanceof ApiError && e.status === 404
            ? t("parental.tier.error.notFound")
            : t("common.error")
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <tr data-testid="parental-review-row">
      <th scope="row">
        <Link to={`/videos/${encodeURIComponent(item.videoId)}`}>
          <bdi>{item.title}</bdi>
        </Link>
        <br />
        <span className="mkt-parental-note">
          <bdi>{item.libraryName}</bdi>
        </span>
      </th>
      <td>
        <Badge tone={TONE[item.state]}>{t(`parental.review.state.${item.state}`)}</Badge>
        {item.state !== "not_checked" && (
          <>
            <br />
            <bdi>{t(tierKey(item.detectedTier))}</bdi>{" "}
            <span className="mkt-parental-note">
              {t("parental.review.sure", { percent: n(Math.round(item.confidence * 100)) })}
            </span>
          </>
        )}
      </td>
      <td>
        {item.categories.length === 0 ? (
          <span className="mkt-parental-note">{t("parental.review.nothingHeard")}</span>
        ) : (
          <ul className="mkt-parental-review__cats">
            {item.categories.map((c) => (
              <li key={c.name}>
                {t(`parental.review.category.${c.name}`)} ({t(`parental.review.level.${c.level}`)})
                {c.evidence.map((at) => (
                  <span key={at}>
                    {" "}
                    <Link
                      to={watch(at)}
                      aria-label={t("parental.review.playAt", { time: formatDuration(at) })}
                    >
                      <bdi>{formatDuration(at)}</bdi>
                    </Link>
                  </span>
                ))}
              </li>
            ))}
          </ul>
        )}
      </td>
      <td>
        <bdi>{t(tierKey(item.tier))}</bdi>
        {item.source && (
          <>
            <br />
            <span className="mkt-parental-note">
              {SOURCES.has(item.source) ? (
                t(`parental.tier.source.${item.source}`)
              ) : (
                <bdi>{item.source}</bdi>
              )}
            </span>
          </>
        )}
      </td>
      {canDecide && (
        <td>
          <div className="mkt-parental-review__decide">
            <select
              value={choice}
              aria-label={t("parental.review.rating", { title: item.title })}
              onChange={(e) => setChoice(e.target.value as typeof choice)}
            >
              <TierOptions clearLabel={t("parental.tier.unrated")} />
            </select>
            <Button
              variant="secondary"
              disabled={busy || choice === ""}
              loading={busy}
              onClick={() => void decide()}
            >
              {choice !== "none" && tierOf(choice) === item.detectedTier
                ? t("parental.review.confirm")
                : t("parental.review.save")}
            </Button>
          </div>
          {error && (
            <p className="mkt-alert" role="alert">
              {error}
            </p>
          )}
        </td>
      )}
    </tr>
  );
}
