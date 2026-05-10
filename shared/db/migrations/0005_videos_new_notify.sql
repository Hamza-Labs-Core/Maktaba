-- +goose Up
-- +goose StatementBegin
--
-- Slot 0005 (Story 1.1 / plan-01-01) — `videos.new` NOTIFY trigger.
--
-- The scanner inserts a row into `videos` for every supported file it
-- discovers (Story 1.1 AC1). The API service LISTENs on `videos.new`
-- and fans the payload out over `/ws/library/{id}` so the UI sees newly
-- discovered videos in real time (AC2: WS frame count == inserted-row
-- count). Because the trigger fires at the SQL layer, the count
-- invariant is enforced by the database — application code cannot drop
-- a frame by forgetting to publish.
--
-- Scope of THIS migration:
--   - `videos_notify_new()` PL/pgSQL function that builds the JSON
--     payload (id, library_id, content_hash, path, filename, state) and
--     calls `pg_notify('videos.new', …)`.
--   - `videos_notify_new_trg` AFTER INSERT trigger on `videos`. Fires
--     once per row.
--
-- Out of scope (other slots own these):
--   - `videos.state_changed` NOTIFY on UPDATE → slot 0004 (plan-01-06).
--   - The `videos` table itself → slot 0001 (plan-01-05).
--   - The WebSocket fan-out on the API side → epic 07.
--
-- Idempotency: `CREATE OR REPLACE FUNCTION` and `DROP TRIGGER IF EXISTS`
-- + `CREATE OR REPLACE TRIGGER` keep re-application a no-op.
--
CREATE OR REPLACE FUNCTION videos_notify_new() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'videos.new',
        json_build_object(
            'id',           NEW.id,
            'library_id',   NEW.library_id,
            'content_hash', NEW.content_hash,
            'path',         NEW.path,
            'filename',     NEW.filename,
            'state',        NEW.state
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS videos_notify_new_trg ON videos;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER videos_notify_new_trg
    AFTER INSERT ON videos
    FOR EACH ROW
    EXECUTE FUNCTION videos_notify_new();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS videos_notify_new_trg ON videos;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS videos_notify_new();
-- +goose StatementEnd
