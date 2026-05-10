// Story 11.3 — Video player.
//
// Phase 10 scaffolds an HTML5 <video> element pointed at the streaming
// manifest endpoint (`/api/videos/{id}/stream`). Story 11.3's full
// implementation (HLS.js, sprite hover-scrubbing, sidecar subtitle
// switching) ships later in Epic 11.
import { useEffect, useRef, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import { useI18n } from '../lib/i18n';

interface StreamManifest {
  manifest_url: string;
  expires_at?: string;
  subtitles?: Array<{ url: string; language: string; label: string }>;
}

export function VideoPlayer() {
  const { videoId } = useParams();
  const nav = useNavigate();
  const { t } = useI18n();
  const [manifest, setManifest] = useState<StreamManifest | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const videoRef = useRef<HTMLVideoElement>(null);

  useEffect(() => {
    if (!videoId) return;
    api
      .get<StreamManifest>(`/api/videos/${encodeURIComponent(videoId)}/stream`)
      .then(setManifest)
      .catch((e) => {
        if (e instanceof ApiError) setErr(e.problem.title);
        else setErr(t('common.error'));
      });
  }, [videoId, t]);

  if (err) return <div className="mkt-alert" role="alert">{err}</div>;
  if (!manifest) return <p>{t('common.loading')}</p>;

  return (
    <section className="mkt-player">
      <button type="button" onClick={() => nav(-1)} className="mkt-btn mkt-btn--ghost">
        ← Back
      </button>
      <video
        ref={videoRef}
        className="mkt-player__video"
        src={manifest.manifest_url}
        controls
        playsInline
        preload="metadata"
      >
        {manifest.subtitles?.map((s) => (
          <track
            key={s.url}
            kind="subtitles"
            srcLang={s.language}
            label={s.label}
            src={s.url}
          />
        ))}
      </video>
    </section>
  );
}
