-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0171 (audio tracks with no codec get their stream's
-- facts from media_info.raw_ffprobe, by audio rank; only rows with no codec, only from a
-- stream that names one). raw_ffprobe is text here: json_each walks its streams; output
-- that is not JSON, or whose streams are not an array of objects, is passed over.
--
UPDATE audio_tracks
   SET (codec, channels, sample_rate, language, title, disposition) = (
        SELECT json_extract(a.s, '$.codec_name'),
               CASE WHEN json_type(a.s, '$.channels') = 'integer' THEN json_extract(a.s, '$.channels')
                    WHEN json_type(a.s, '$.channels') = 'text' AND json_extract(a.s, '$.channels') <> ''
                         AND json_extract(a.s, '$.channels') NOT GLOB '*[^0-9]*'
                    THEN CAST(json_extract(a.s, '$.channels') AS INTEGER) END,
               CASE WHEN json_type(a.s, '$.sample_rate') = 'integer' THEN json_extract(a.s, '$.sample_rate')
                    WHEN json_type(a.s, '$.sample_rate') = 'text' AND json_extract(a.s, '$.sample_rate') <> ''
                         AND json_extract(a.s, '$.sample_rate') NOT GLOB '*[^0-9]*'
                    THEN CAST(json_extract(a.s, '$.sample_rate') AS INTEGER) END,
               COALESCE(NULLIF(json_extract(a.s, '$.tags.language'), ''), 'und'),
               json_extract(a.s, '$.tags.title'),
               CASE WHEN json_type(a.s, '$.disposition') = 'object'
                    THEN json_extract(a.s, '$.disposition') ELSE '{}' END
          FROM (SELECT e.value AS s, row_number() OVER (ORDER BY e.key) - 1 AS rank
                  FROM media_info mi,
                       json_each(CASE WHEN NOT json_valid(mi.raw_ffprobe) THEN '{}'
                                      WHEN json_type(mi.raw_ffprobe, '$.streams') = 'array'
                                      THEN mi.raw_ffprobe ELSE '{}' END, '$.streams') e
                 WHERE mi.video_id = audio_tracks.video_id
                   AND CASE WHEN e.type = 'object'
                            THEN json_extract(e.value, '$.codec_type') END = 'audio') a
         WHERE a.rank = audio_tracks.track_index)
 WHERE COALESCE(codec, '') = ''
   AND COALESCE((
        SELECT json_extract(a.s, '$.codec_name')
          FROM (SELECT e.value AS s, row_number() OVER (ORDER BY e.key) - 1 AS rank
                  FROM media_info mi,
                       json_each(CASE WHEN NOT json_valid(mi.raw_ffprobe) THEN '{}'
                                      WHEN json_type(mi.raw_ffprobe, '$.streams') = 'array'
                                      THEN mi.raw_ffprobe ELSE '{}' END, '$.streams') e
                 WHERE mi.video_id = audio_tracks.video_id
                   AND CASE WHEN e.type = 'object'
                            THEN json_extract(e.value, '$.codec_type') END = 'audio') a
         WHERE a.rank = audio_tracks.track_index), '') <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Nothing to undo: the filled facts are the probe's, which it writes again on any probe.
SELECT 1;
-- +goose StatementEnd
