-- +goose Up
-- +goose StatementBegin
--
-- Slot 0171 - audio tracks with no codec get their stream's facts from the probe's output.
--
-- Transcribe records the stream it decoded as track 0 when the video has no track row
-- (engine/stages/transcribe.py, since 2026-10-17): a bare row, no codec, channels or
-- sample rate. When that came before the probe, the probe's insert did nothing on the
-- conflict, and streaming read the codec-less default track as "no audio": the video
-- played silent on every player. The probe now updates on conflict; this fills the rows
-- already written.
--
-- A track's facts are its audio stream in media_info.raw_ffprobe by audio rank (the Nth
-- stream whose codec_type is audio is track_index N - 1, ffmpeg's -map 0:a:N), as the
-- probe reads them (audio/probe.py parse_ffprobe_json): codec_name, channels,
-- sample_rate, tags.language ('und' when absent), tags.title, disposition. is_default is
-- left as it is. Only rows with no codec are written, and only from a stream that names
-- one: run twice, the second run finds nothing to do.
--
UPDATE audio_tracks t
   SET codec       = a.s->>'codec_name',
       channels    = CASE WHEN a.s->>'channels' ~ '^[0-9]{1,9}$' THEN (a.s->>'channels')::int END,
       sample_rate = CASE WHEN a.s->>'sample_rate' ~ '^[0-9]{1,9}$' THEN (a.s->>'sample_rate')::int END,
       language    = COALESCE(NULLIF(a.s->'tags'->>'language', ''), 'und'),
       title       = a.s->'tags'->>'title',
       disposition = CASE WHEN jsonb_typeof(a.s->'disposition') = 'object'
                          THEN a.s->'disposition' ELSE '{}'::jsonb END
  FROM media_info mi
  CROSS JOIN LATERAL (
        SELECT e.s, (row_number() OVER (ORDER BY e.n) - 1)::int AS rank
          FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mi.raw_ffprobe->'streams') = 'array'
                                         THEN mi.raw_ffprobe->'streams' ELSE '[]'::jsonb END)
               WITH ORDINALITY AS e(s, n)
         WHERE e.s->>'codec_type' = 'audio'
  ) a
 WHERE mi.video_id = t.video_id
   AND a.rank = t.track_index
   AND COALESCE(t.codec, '') = ''
   AND COALESCE(a.s->>'codec_name', '') <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Nothing to undo: the filled facts are the probe's, which it writes again on any probe.
SELECT 1;
-- +goose StatementEnd
