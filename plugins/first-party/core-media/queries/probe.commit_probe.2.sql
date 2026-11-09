-- name: probe.commit_probe.2
-- mode: write
-- scope: subject $1
-- tables: audio_tracks*
INSERT INTO audio_tracks
       (video_id, track_index, codec, channels, sample_rate, language, title,
        is_default, disposition)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (video_id, track_index) DO UPDATE SET
    codec        = EXCLUDED.codec,
    channels     = EXCLUDED.channels,
    sample_rate  = EXCLUDED.sample_rate,
    language     = EXCLUDED.language,
    title        = EXCLUDED.title,
    is_default   = EXCLUDED.is_default,
    disposition  = EXCLUDED.disposition
