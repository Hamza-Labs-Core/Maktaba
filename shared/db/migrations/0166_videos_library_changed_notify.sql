-- +goose Up
-- +goose StatementBegin
--
-- Slot 0166: every API process hears when a library's rows change.
--
-- The API keeps each library's newest videos in memory (the pinned libraries'
-- rows on the home, a library hub's recently added; api/internal/handlers/browse
-- rowcache.go) and must drop them when the library's videos change, on every
-- machine the API runs on. The videos are written by the pipeline (scan, probe,
-- the missing-files sweep, merges), by the API and by hand, so the database
-- says so, as slot 0005 does for new videos: `pg_notify('browse.library_changed',
-- <library id>)` for
--
--   * a video inserted or deleted (a library deleted removes its videos);
--   * a change of a column a row's card shows or its predicate reads: library_id
--     (both libraries), title, filename, duration_sec, state, created_at, mtime,
--     published_at, deleted_at;
--
-- and '*' (every library) for a TRUNCATE. Other columns (metadata, paths, the
-- pipeline's bookkeeping) never notify. Postgres folds the same notification
-- within one transaction, so a scan's batch sends one per library.
--
-- Additive.
--
CREATE OR REPLACE FUNCTION videos_notify_library_changed() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'TRUNCATE' THEN
        PERFORM pg_notify('browse.library_changed', '*');
        RETURN NULL;
    END IF;
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        PERFORM pg_notify('browse.library_changed', OLD.library_id::text);
    END IF;
    IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND NEW.library_id IS DISTINCT FROM OLD.library_id) THEN
        PERFORM pg_notify('browse.library_changed', NEW.library_id::text);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER videos_library_changed_ins_del_trg
    AFTER INSERT OR DELETE ON videos
    FOR EACH ROW
    EXECUTE FUNCTION videos_notify_library_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER videos_library_changed_upd_trg
    AFTER UPDATE OF library_id, title, filename, duration_sec, state, created_at, mtime, published_at, deleted_at
    ON videos
    FOR EACH ROW
    WHEN ((OLD.library_id, OLD.title, OLD.filename, OLD.duration_sec, OLD.state, OLD.created_at, OLD.mtime,
           OLD.published_at, OLD.deleted_at)
          IS DISTINCT FROM
          (NEW.library_id, NEW.title, NEW.filename, NEW.duration_sec, NEW.state, NEW.created_at, NEW.mtime,
           NEW.published_at, NEW.deleted_at))
    EXECUTE FUNCTION videos_notify_library_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER videos_library_changed_truncate_trg
    AFTER TRUNCATE ON videos
    FOR EACH STATEMENT
    EXECUTE FUNCTION videos_notify_library_changed();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS videos_library_changed_truncate_trg ON videos;
DROP TRIGGER IF EXISTS videos_library_changed_upd_trg ON videos;
DROP TRIGGER IF EXISTS videos_library_changed_ins_del_trg ON videos;
DROP FUNCTION IF EXISTS videos_notify_library_changed();
-- +goose StatementEnd
