-- name: transcribe.first_audio_track
-- mode: write
-- scope: subject $1
-- tables: audio_tracks*
INSERT INTO audio_tracks (video_id, track_index, codec, channels, sample_rate,                          language, title, disposition, is_default) VALUES ($1, 0, $2, $3, $4, $5, $6, $7, NOT EXISTS (SELECT 1 FROM audio_tracks  WHERE video_id = $1 AND is_default)) ON CONFLICT (video_id, track_index) DO UPDATE SET  codec = COALESCE(NULLIF(audio_tracks.codec, ''), EXCLUDED.codec),  channels = COALESCE(audio_tracks.channels, EXCLUDED.channels),  sample_rate = COALESCE(audio_tracks.sample_rate, EXCLUDED.sample_rate) RETURNING id
