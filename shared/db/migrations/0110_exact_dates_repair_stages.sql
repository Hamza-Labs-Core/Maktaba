-- +goose Up
-- +goose StatementBegin
--
-- Slot 0106 (plan 06-11, mediaintel parity) - register the exact_dates and repair_broken
-- queues and the pools of the channel audit and the repair.
--
-- exact_dates: per-video exact upload dates for series members whose date is only
-- approximate (mediaintel "exact" step); follows youtube_sync, then detect again. Off until
-- the "exactdates" rules say enabled.
-- repair_broken: files whose probe failed are fetched from YouTube again and swapped in on the
-- share (mediaintel repair-broken). Off until the "repair" rules say enabled; its own pool, so
-- a download of an hour never holds the YouTube listing.
-- audits: the dizqueTV channel audit (extension stage dizquetv_audit, synced from its
-- extension), apart from the pool that writes the channels.
--
-- Workers re-sync job_stages from the graph at start (engine/sync.py); this seed keeps a fresh
-- database consistent before the first worker runs, as slot 0093 does.
--
INSERT INTO worker_pools (pool, label, scope, size, default_size) VALUES
    ('audits', 'Channel audit', 'global', 1, 1),
    ('repairs', 'Repairs', 'global', 1, 1)
ON CONFLICT (pool) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode)
VALUES
    ('exact_dates', 'Exact dates', ARRAY['library']::text[], 'network_yt', 'sources', 1, 1, 300, 3600, 3, '[{"to":"detect","map":"same","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['library']::text[], false, false, false, 'auto'),
    ('repair_broken', 'Repair broken files', ARRAY['library']::text[], 'repairs', 'media', 10, 1, 0, 0, 1, '[]'::jsonb, ARRAY['probe','content_hash']::text[], '{}'::text[], false, false, true, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"link","map":"videos_of","when":"output_changed","legacy":false},{"to":"detect","map":"same","when":"output_changed","legacy":false},{"to":"exact_dates","map":"same","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'youtube_sync';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"link","map":"videos_of","when":"output_changed","legacy":false},{"to":"detect","map":"same","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'youtube_sync';
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM job_stages WHERE stage IN ('exact_dates', 'repair_broken')
   AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE stage IN ('exact_dates', 'repair_broken'));
-- +goose StatementEnd
