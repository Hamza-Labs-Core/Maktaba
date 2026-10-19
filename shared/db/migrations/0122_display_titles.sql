-- +goose Up
-- +goose StatementBegin
--
-- Slot 0122 (Epic 27, Story 27.11: channel strip and display titles) - the short, clean
-- on-air title of every video and series, and the display_title stage that makes them.
--
--   videos.display_title         what the rules (or the language model) made of the
--                                library title: no channel names, hashtags, emojis, links,
--                                dates or file leftovers; the series and episode apart
--   videos.display_title_source  'rules' | 'llm'
--   videos.display_title_manual  set by a person in the library; always wins
--   series.display_name          the series' name cleaned the same way
--   display_title_llm            the model's answers, by input hash (a title is asked once)
--
-- The guide, XMLTV, the dizqueTV export and the streaming strip read the display titles
-- from channel_programs.title_snapshot (keys on_air, strip_title, strip_series,
-- strip_episode; title/series/episode with display_titles.use_in_guide). Workers re-sync
-- job_stages from the graph at start; this seed keeps a fresh database consistent first.
--
ALTER TABLE videos ADD COLUMN IF NOT EXISTS display_title TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS display_title_source TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS display_title_manual TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE series ADD COLUMN IF NOT EXISTS display_name TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS display_title_llm (
    input_hash  TEXT        PRIMARY KEY,
    model       TEXT        NOT NULL,
    title       TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode)
VALUES
    ('display_title', 'Display titles', ARRAY['library']::text[], 'cpu', 'seasons', 4, 1, 30, 300, 3, '[]'::jsonb, '{}'::text[], ARRAY['library']::text[], false, false, false, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"tag","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"rate_style","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"tag","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"review_pages","map":"same","when":"output_changed","legacy":false},{"to":"topic_collections","map":"same","when":"output_changed","legacy":false},{"to":"promo_links","map":"same","when":"output_changed","legacy":false},{"to":"display_title","map":"same","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'detect';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"tag","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"rate_style","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"tag","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"review_pages","map":"same","when":"output_changed","legacy":false},{"to":"topic_collections","map":"same","when":"output_changed","legacy":false},{"to":"promo_links","map":"same","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'detect';
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'display_title'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE stage = 'display_title');
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS display_title_llm;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE series DROP COLUMN IF EXISTS display_name;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS display_title_manual;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS display_title_source;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS display_title;
-- +goose StatementEnd
