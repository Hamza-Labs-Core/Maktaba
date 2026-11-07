-- name: probe.commit_probe
-- mode: write
-- scope: subject $1
-- tables: media_info*
INSERT INTO media_info
       (video_id, container, video_codec, width, height, fps, bitrate_kbps,
        has_subtitles, raw_ffprobe)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (video_id) DO UPDATE SET
    container       = EXCLUDED.container,
    video_codec     = EXCLUDED.video_codec,
    width           = EXCLUDED.width,
    height          = EXCLUDED.height,
    fps             = EXCLUDED.fps,
    bitrate_kbps    = EXCLUDED.bitrate_kbps,
    has_subtitles   = EXCLUDED.has_subtitles,
    raw_ffprobe     = EXCLUDED.raw_ffprobe,
    probed_at       = now()
