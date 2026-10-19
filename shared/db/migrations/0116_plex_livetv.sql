-- +goose Up
-- +goose StatementBegin
--
-- Slot 0116 (Epic 27, P0-1): Plex Live TV integration. One row (id = 1) holding what the
-- Plex extension's plex_livetv stage last saw and did in Plex:
--
--   register_requested_at  an admin asked "Add Maktaba to Plex" (POST /api/tv/plex/register);
--                          the stage registers the tuner + DVR once and clears it
--   requested_base_url     the tuner address the API reported when that was asked (used
--                          when neither rules.plexlive.tuner_url nor MAKTABA_PUBLIC_BASE_URL
--                          is set where the stage runs)
--   status                 the last report: Plex reachable, DVRs and tuners found, which one
--                          is Maktaba's, channels mapped, the last action's outcome
--   guide_reloaded_at /    the last POST /livetv/dvrs/{key}/reloadGuide and the schedule
--   guide_version          generation it covered (max channel_schedule_state.last_generated_at)
--   channelmap_at /        the last PUT /media/grabbers/devices/{key}/channelmap and the
--   channelmap_hash        lineup it pushed
--
-- Read by GET /api/tv/plex; written only by the stage (and the register request by the API).
--
CREATE TABLE IF NOT EXISTS plex_livetv_state (
    id                     SMALLINT    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    register_requested_at  TIMESTAMPTZ,
    requested_base_url     TEXT,
    status                 JSONB       NOT NULL DEFAULT '{}'::jsonb,
    checked_at             TIMESTAMPTZ,
    guide_reloaded_at      TIMESTAMPTZ,
    guide_version          TEXT,
    channelmap_at          TIMESTAMPTZ,
    channelmap_hash        TEXT,
    last_error             TEXT,
    last_error_at          TIMESTAMPTZ,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plex_livetv_state;
-- +goose StatementEnd
