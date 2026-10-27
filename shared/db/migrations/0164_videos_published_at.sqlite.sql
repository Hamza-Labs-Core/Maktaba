-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0164: videos.published_at (TEXT, the canonical
-- ISO time at midnight UTC) is the upload date of the video's best YouTube match,
-- kept in step by triggers; SQLite has no functions, so each trigger spells the
-- rule out (the same as video_published_at() in the Postgres file).
--
ALTER TABLE videos ADD COLUMN IF NOT EXISTS published_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS media_metadata_enrichment_published_at_ins
    AFTER INSERT ON media_metadata_enrichment
    FOR EACH ROW WHEN NEW.provider = 'youtube'
    BEGIN
        UPDATE videos SET published_at = (SELECT substr(y.upload_date, 1, 10) || 'T00:00:00.000Z'
              FROM media_metadata_enrichment m
              LEFT JOIN youtube_videos y ON y.youtube_id = CASE WHEN m.external_id LIKE 'youtube:%'
                                                                THEN substr(m.external_id, 9)
                                                                ELSE m.external_id END
             WHERE m.video_id = videos.id AND m.provider = 'youtube'
             ORDER BY m.confidence DESC, m.created_at DESC, m.id
             LIMIT 1)
         WHERE id = NEW.video_id;
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS media_metadata_enrichment_published_at_upd
    AFTER UPDATE OF video_id, provider, external_id, confidence, created_at ON media_metadata_enrichment
    FOR EACH ROW WHEN OLD.provider = 'youtube' OR NEW.provider = 'youtube'
    BEGIN
        UPDATE videos SET published_at = (SELECT substr(y.upload_date, 1, 10) || 'T00:00:00.000Z'
              FROM media_metadata_enrichment m
              LEFT JOIN youtube_videos y ON y.youtube_id = CASE WHEN m.external_id LIKE 'youtube:%'
                                                                THEN substr(m.external_id, 9)
                                                                ELSE m.external_id END
             WHERE m.video_id = videos.id AND m.provider = 'youtube'
             ORDER BY m.confidence DESC, m.created_at DESC, m.id
             LIMIT 1)
         WHERE id IN (OLD.video_id, NEW.video_id);
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS media_metadata_enrichment_published_at_del
    AFTER DELETE ON media_metadata_enrichment
    FOR EACH ROW WHEN OLD.provider = 'youtube'
    BEGIN
        UPDATE videos SET published_at = (SELECT substr(y.upload_date, 1, 10) || 'T00:00:00.000Z'
              FROM media_metadata_enrichment m
              LEFT JOIN youtube_videos y ON y.youtube_id = CASE WHEN m.external_id LIKE 'youtube:%'
                                                                THEN substr(m.external_id, 9)
                                                                ELSE m.external_id END
             WHERE m.video_id = videos.id AND m.provider = 'youtube'
             ORDER BY m.confidence DESC, m.created_at DESC, m.id
             LIMIT 1)
         WHERE id = OLD.video_id;
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS youtube_videos_published_at_ins
    AFTER INSERT ON youtube_videos
    FOR EACH ROW WHEN NEW.upload_date IS NOT NULL
    BEGIN
        UPDATE videos SET published_at = (SELECT substr(y.upload_date, 1, 10) || 'T00:00:00.000Z'
              FROM media_metadata_enrichment m
              LEFT JOIN youtube_videos y ON y.youtube_id = CASE WHEN m.external_id LIKE 'youtube:%'
                                                                THEN substr(m.external_id, 9)
                                                                ELSE m.external_id END
             WHERE m.video_id = videos.id AND m.provider = 'youtube'
             ORDER BY m.confidence DESC, m.created_at DESC, m.id
             LIMIT 1)
         WHERE id IN (SELECT video_id FROM media_metadata_enrichment
                       WHERE provider = 'youtube' AND (external_id IN (NEW.youtube_id, 'youtube:' || NEW.youtube_id)));
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS youtube_videos_published_at_upd
    AFTER UPDATE OF upload_date, youtube_id ON youtube_videos
    FOR EACH ROW WHEN OLD.upload_date IS NOT NEW.upload_date OR OLD.youtube_id <> NEW.youtube_id
    BEGIN
        UPDATE videos SET published_at = (SELECT substr(y.upload_date, 1, 10) || 'T00:00:00.000Z'
              FROM media_metadata_enrichment m
              LEFT JOIN youtube_videos y ON y.youtube_id = CASE WHEN m.external_id LIKE 'youtube:%'
                                                                THEN substr(m.external_id, 9)
                                                                ELSE m.external_id END
             WHERE m.video_id = videos.id AND m.provider = 'youtube'
             ORDER BY m.confidence DESC, m.created_at DESC, m.id
             LIMIT 1)
         WHERE id IN (SELECT video_id FROM media_metadata_enrichment
                       WHERE provider = 'youtube' AND (external_id IN (OLD.youtube_id, 'youtube:' || OLD.youtube_id) OR external_id IN (NEW.youtube_id, 'youtube:' || NEW.youtube_id)));
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS youtube_videos_published_at_del
    AFTER DELETE ON youtube_videos
    FOR EACH ROW WHEN OLD.upload_date IS NOT NULL
    BEGIN
        UPDATE videos SET published_at = (SELECT substr(y.upload_date, 1, 10) || 'T00:00:00.000Z'
              FROM media_metadata_enrichment m
              LEFT JOIN youtube_videos y ON y.youtube_id = CASE WHEN m.external_id LIKE 'youtube:%'
                                                                THEN substr(m.external_id, 9)
                                                                ELSE m.external_id END
             WHERE m.video_id = videos.id AND m.provider = 'youtube'
             ORDER BY m.confidence DESC, m.created_at DESC, m.id
             LIMIT 1)
         WHERE id IN (SELECT video_id FROM media_metadata_enrichment
                       WHERE provider = 'youtube' AND (external_id IN (OLD.youtube_id, 'youtube:' || OLD.youtube_id)));
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS media_metadata_enrichment_youtube_idx
    ON media_metadata_enrichment (external_id) WHERE provider = 'youtube';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE videos SET published_at = (SELECT substr(y.upload_date, 1, 10) || 'T00:00:00.000Z'
              FROM media_metadata_enrichment m
              LEFT JOIN youtube_videos y ON y.youtube_id = CASE WHEN m.external_id LIKE 'youtube:%'
                                                                THEN substr(m.external_id, 9)
                                                                ELSE m.external_id END
             WHERE m.video_id = videos.id AND m.provider = 'youtube'
             ORDER BY m.confidence DESC, m.created_at DESC, m.id
             LIMIT 1)
 WHERE id IN (SELECT video_id FROM media_metadata_enrichment WHERE provider = 'youtube');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS videos_library_published_idx
    ON videos (library_id, COALESCE(published_at, mtime) DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_library_published_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS media_metadata_enrichment_youtube_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS youtube_videos_published_at_del;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS youtube_videos_published_at_upd;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS youtube_videos_published_at_ins;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS media_metadata_enrichment_published_at_del;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS media_metadata_enrichment_published_at_upd;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS media_metadata_enrichment_published_at_ins;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN published_at;
-- +goose StatementEnd
