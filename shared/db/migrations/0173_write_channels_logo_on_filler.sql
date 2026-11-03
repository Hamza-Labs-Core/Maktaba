-- +goose Up
-- +goose StatementBegin
--
-- Slot 0173 - write_channels version 5: rules tv.watermark_skip_filler is off by default
-- (the channel's corner logo airs on every block, filler and bumpers too). The seed
-- matches the graph (engine/graph.py CH_WRITE_VERSION); the version sweep compares each
-- done job's stage_version with the graph, so write_channels still runs again on the
-- update and re-stages every channel whose stored look leaves the logo off filler.
--
UPDATE job_stages SET version = 5 WHERE stage = 'write_channels' AND version < 5;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 4 WHERE stage = 'write_channels' AND version = 5;
-- +goose StatementEnd
