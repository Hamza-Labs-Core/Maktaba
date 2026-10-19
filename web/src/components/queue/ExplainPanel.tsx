// "Explain a video": why is this video where it is. Type part of a title and
// see, per downloaded video, its air date and how sure that date is, length,
// playlists and positions, how it was linked to YouTube, its Plex item and the
// detector's verdict (GET /api/explain). Videos on the channel that were never
// downloaded are listed apart.
import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type {
  ExplainPlaylist,
  ExplainRemote,
  ExplainResponse,
  ExplainVideo,
  LibraryQueueRow,
} from "../../lib/queueApi";
import { useAction } from "./useAction";
import { useFmt } from "./format";

const MIN_CHARS = 2;

function Playlists({ list }: { list: ExplainPlaylist[] }) {
  const { t, n } = useI18n();
  if (list.length === 0) return null;
  return (
    <>
      {list.map((p) => (
        <div key={`${p.title}:${p.position}`}>
          <dt>{t("queue.explain.playlist")}</dt>
          <dd dir="auto">
            {bidiIsolate(p.title)}
            {p.position !== null && ` #${n(p.position)}`}
          </dd>
        </div>
      ))}
    </>
  );
}

function Aired({ date, quality }: { date: string | null; quality: string | null }) {
  const { t } = useI18n();
  if (!date) return <>{t("queue.explain.noDate")}</>;
  return (
    <>
      {date} ({quality ? t(`queue.explain.quality.${quality}`) : t("queue.explain.noDate")})
    </>
  );
}

function VideoCard({ v }: { v: ExplainVideo }) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  return (
    <li className="mkt-q-explain__item">
      <h3 dir="auto">
        <Link to={`/videos/${encodeURIComponent(v.id)}`}>{bidiIsolate(v.title)}</Link>
      </h3>
      <dl className="mkt-q-explain__facts">
        <div>
          <dt>{t("queue.explain.file")}</dt>
          <dd dir="auto">
            {bidiIsolate(v.library)} / {bidiIsolate(v.file)}
          </dd>
        </div>
        <div>
          <dt>{t("queue.explain.aired")}</dt>
          <dd>
            <Aired date={v.aired} quality={v.date_quality} />
          </dd>
        </div>
        <div>
          <dt>{t("queue.explain.length")}</dt>
          <dd>
            {v.duration_sec ? fmt.dur(v.duration_sec) : "?"}
            {v.width && v.height
              ? ` · ${n(v.width, { useGrouping: false })}×${n(v.height, { useGrouping: false })}`
              : ""}
          </dd>
        </div>
        <div>
          <dt>{t("queue.explain.youtube")}</dt>
          <dd>
            {v.youtube_id ?? "–"}
            {v.link_method ? ` · ${t("queue.explain.via", { method: v.link_method })}` : ""}
            {v.tab ? ` · ${v.tab}` : ""}
          </dd>
        </div>
        <Playlists list={v.playlists} />
        <div>
          <dt>{t("queue.explain.plex")}</dt>
          <dd>{v.plex_rating_key ?? "–"}</dd>
        </div>
        <div>
          <dt>{t("queue.explain.verdict")}</dt>
          <dd
            className={`mkt-q-explain__verdict mkt-q-explain__verdict--${v.verdict.kind}`}
            dir="auto"
          >
            {v.verdict.season_id ? (
              <Link to={`/queue/items/season/${encodeURIComponent(v.verdict.season_id)}`}>
                {bidiIsolate(v.verdict.label)}
              </Link>
            ) : (
              bidiIsolate(v.verdict.label)
            )}
          </dd>
        </div>
      </dl>
    </li>
  );
}

function RemoteCard({ v }: { v: ExplainRemote }) {
  const { t } = useI18n();
  const fmt = useFmt();
  return (
    <li className="mkt-q-explain__item mkt-q-explain__item--remote">
      <h3 dir="auto">{bidiIsolate(v.title)}</h3>
      <p className="mkt-q-muted">
        <Aired date={v.aired} quality={v.date_quality} /> ·{" "}
        {v.duration_sec ? fmt.dur(v.duration_sec) : "?"}
        {v.tab ? ` · ${v.tab}` : ""} ·{" "}
        <a
          href={`https://youtu.be/${encodeURIComponent(v.youtube_id)}`}
          target="_blank"
          rel="noopener noreferrer"
        >
          {v.youtube_id}
        </a>
      </p>
      {v.playlists.length > 0 && (
        <p className="mkt-q-muted" dir="auto">
          {t("queue.explain.playlist")}:{" "}
          {v.playlists
            .map((p) => `${bidiIsolate(p.title)}${p.position !== null ? ` #${p.position}` : ""}`)
            .join(" / ")}
        </p>
      )}
    </li>
  );
}

export function ExplainPanel({ libraries }: { libraries: LibraryQueueRow[] }) {
  const { t, n } = useI18n();
  const { busy, error, run } = useAction();
  const [text, setText] = useState("");
  const [library, setLibrary] = useState("");
  const [result, setResult] = useState<ExplainResponse | null>(null);
  const tooShort = text.trim().length < MIN_CHARS;

  async function search() {
    if (tooShort) return;
    const r = await run("explain", () => queueApi.explain(text.trim(), library || undefined));
    if (r) setResult(r);
  }

  return (
    <section className="mkt-q-explain" aria-labelledby="mkt-q-explain-h">
      <h2 className="mkt-q-section-title" id="mkt-q-explain-h">
        {t("queue.explain.title")}
      </h2>
      <p className="mkt-q-muted">{t("queue.explain.intro")}</p>
      <form
        className="mkt-q-row"
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          void search();
        }}
      >
        <input
          type="search"
          dir="auto"
          value={text}
          placeholder={t("queue.explain.placeholder")}
          aria-label={t("queue.explain.q")}
          onChange={(e) => setText(e.target.value)}
        />
        <select
          value={library}
          aria-label={t("queue.explain.library")}
          onChange={(e) => setLibrary(e.target.value)}
        >
          <option value="">{t("queue.explain.anyLibrary")}</option>
          {libraries.map((l) => (
            <option key={l.library_id} value={l.library_id}>
              {l.name}
            </option>
          ))}
        </select>
        <Button type="submit" loading={busy === "explain"} disabled={tooShort}>
          {t("queue.explain.search")}
        </Button>
      </form>
      {error && (
        <p role="alert" className="mkt-q-error">
          {error}
        </p>
      )}
      {result && (
        <div aria-live="polite">
          {result.remote.length > 0 && (
            <>
              <h3 className="mkt-q-eyebrow">
                {t("queue.explain.remote", { n: n(result.remote.length) })}
              </h3>
              <ul className="mkt-q-explain__list">
                {result.remote.map((v) => (
                  <RemoteCard key={v.youtube_id} v={v} />
                ))}
              </ul>
            </>
          )}
          {result.videos.length === 0 ? (
            <p className="mkt-q-empty">{t("queue.explain.none")}</p>
          ) : (
            <>
              <h3 className="mkt-q-eyebrow">
                {t("queue.explain.found", { n: n(result.videos.length) })}
              </h3>
              <ul className="mkt-q-explain__list">
                {result.videos.map((v) => (
                  <VideoCard key={v.id} v={v} />
                ))}
              </ul>
            </>
          )}
        </div>
      )}
    </section>
  );
}
