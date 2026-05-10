// Story 11.2 — Video detail page.
import { useEffect, useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import { useI18n } from '../lib/i18n';

interface Video {
  id: string;
  title: string;
  description?: string;
  duration_sec?: number;
  library_id?: string;
  speakers?: Array<{ id: string; name: string }>;
  chapters?: Array<{ start_sec: number; title: string }>;
}

export function VideoDetail() {
  const { videoId } = useParams();
  const { t } = useI18n();
  const [video, setVideo] = useState<Video | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!videoId) return;
    setVideo(null);
    setErr(null);
    api
      .get<Video>(`/api/videos/${encodeURIComponent(videoId)}`)
      .then(setVideo)
      .catch((e) => {
        if (e instanceof ApiError) setErr(e.problem.title);
        else setErr(t('common.error'));
      });
  }, [videoId, t]);

  if (err) return <div className="mkt-alert" role="alert">{err}</div>;
  if (!video) return <p>{t('common.loading')}</p>;

  return (
    <section className="mkt-page mkt-video">
      <h1>{video.title}</h1>
      {video.description && <p className="mkt-video__desc">{video.description}</p>}
      <Link to={`/videos/${video.id}/watch`} className="mkt-btn mkt-btn--primary">
        ▶ Play
      </Link>
      {video.chapters && video.chapters.length > 0 && (
        <section aria-labelledby="chapters-heading">
          <h2 id="chapters-heading">Chapters</h2>
          <ol className="mkt-chapters">
            {video.chapters.map((c) => (
              <li key={c.start_sec}>
                <span className="mkt-chapters__time">{c.start_sec}s</span>
                <span>{c.title}</span>
              </li>
            ))}
          </ol>
        </section>
      )}
      {video.speakers && video.speakers.length > 0 && (
        <section aria-labelledby="speakers-heading">
          <h2 id="speakers-heading">Speakers</h2>
          <ul className="mkt-speakers">
            {video.speakers.map((sp) => <li key={sp.id}>{sp.name}</li>)}
          </ul>
        </section>
      )}
    </section>
  );
}
