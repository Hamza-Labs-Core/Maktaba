-- +goose Up
-- +goose StatementBegin
--
-- Slot 0218 - the in-process fallbacks, counted (Epic 54, story 54.43).
--
--   stage_fallbacks  per machine (a cluster node's id, or 'home'), stage and reason: how
--                    many jobs (or fingerprints) of a first-party stage ran in the pipeline
--                    worker instead of its plugin runner, and when the first and the latest
--                    were counted. The worker adds what it counted at each 30 s refresh and
--                    when it stops. A stage an admin set to inproc is not counted.
--
-- Joined machines write it with the default grants of maktaba_worker (slot 0103). Additive.
--
CREATE TABLE IF NOT EXISTS stage_fallbacks (
    machine  text        NOT NULL,
    stage    text        NOT NULL,
    reason   text        NOT NULL,
    count    bigint      NOT NULL DEFAULT 0,
    first_at timestamptz NOT NULL DEFAULT now(),
    last_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (machine, stage, reason)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS stage_fallbacks;
-- +goose StatementEnd
