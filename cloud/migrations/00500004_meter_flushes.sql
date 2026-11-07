-- +goose Up
-- Story 50.05: the relay's meter writes each flush batch once. A batch carries an id; a flush
-- whose COMMIT reached the database but whose answer was lost (a connection cut during a
-- failover) is retried under the same id and found here, so its bytes are not counted twice.
-- Rows older than a day are deleted by the flush itself.
CREATE TABLE IF NOT EXISTS relay_meter_flushes (
    id UUID        PRIMARY KEY,
    at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS relay_meter_flushes;
