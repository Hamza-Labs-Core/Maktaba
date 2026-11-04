-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0175 (the queue reads stop scanning the jobs' history):
-- processing_done_subjects and the triggers that keep it (row triggers: SQLite has no
-- statement trigger, so a deleted done row checks its own subject), the live, done and
-- awaiting-review indexes (no INCLUDE in SQLite), and the backfill.
--
CREATE TABLE IF NOT EXISTS processing_done_subjects (
    library_id   TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    stage        TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    subject_kind TEXT NOT NULL,
    last_at      TEXT NOT NULL,
    PRIMARY KEY (library_id, stage, subject_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS processing_done_subjects_ins_trg
    AFTER INSERT ON processing_jobs
    FOR EACH ROW
    WHEN NEW.state = 'done' AND NEW.library_id IS NOT NULL AND NEW.subject_id IS NOT NULL
BEGIN
    INSERT OR IGNORE INTO processing_done_subjects (library_id, stage, subject_id, subject_kind, last_at)
    VALUES (NEW.library_id, NEW.stage, NEW.subject_id, NEW.subject_kind,
            COALESCE(NEW.finished_at, NEW.created_at));
    UPDATE processing_done_subjects SET last_at = COALESCE(NEW.finished_at, NEW.created_at)
     WHERE library_id = NEW.library_id AND stage = NEW.stage AND subject_id = NEW.subject_id
       AND last_at < COALESCE(NEW.finished_at, NEW.created_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS processing_done_subjects_upd_trg
    AFTER UPDATE OF state, library_id, finished_at, subject_id ON processing_jobs
    FOR EACH ROW
    WHEN NEW.state = 'done' AND NEW.library_id IS NOT NULL AND NEW.subject_id IS NOT NULL
BEGIN
    INSERT OR IGNORE INTO processing_done_subjects (library_id, stage, subject_id, subject_kind, last_at)
    VALUES (NEW.library_id, NEW.stage, NEW.subject_id, NEW.subject_kind,
            COALESCE(NEW.finished_at, NEW.created_at));
    UPDATE processing_done_subjects SET last_at = COALESCE(NEW.finished_at, NEW.created_at)
     WHERE library_id = NEW.library_id AND stage = NEW.stage AND subject_id = NEW.subject_id
       AND last_at < COALESCE(NEW.finished_at, NEW.created_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS processing_done_subjects_left_trg
    AFTER UPDATE OF state, library_id ON processing_jobs
    FOR EACH ROW
    WHEN OLD.state = 'done' AND OLD.library_id IS NOT NULL
         AND (NEW.state IS NOT 'done' OR NEW.library_id IS NOT OLD.library_id)
BEGIN
    DELETE FROM processing_done_subjects
     WHERE library_id = OLD.library_id AND stage = OLD.stage AND subject_id = OLD.subject_id
       AND NOT EXISTS (SELECT 1 FROM processing_jobs p
                        WHERE p.subject_kind = OLD.subject_kind AND p.subject_id = OLD.subject_id
                          AND p.stage = OLD.stage AND p.state = 'done'
                          AND p.library_id = OLD.library_id);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS processing_done_subjects_gone_trg
    AFTER DELETE ON processing_jobs
    FOR EACH ROW
    WHEN OLD.state = 'done' AND OLD.library_id IS NOT NULL
BEGIN
    DELETE FROM processing_done_subjects
     WHERE library_id = OLD.library_id AND stage = OLD.stage AND subject_id = OLD.subject_id
       AND NOT EXISTS (SELECT 1 FROM processing_jobs p
                        WHERE p.subject_kind = OLD.subject_kind AND p.subject_id = OLD.subject_id
                          AND p.stage = OLD.stage AND p.state = 'done'
                          AND p.library_id = OLD.library_id);
END;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO processing_done_subjects (library_id, stage, subject_id, subject_kind, last_at)
SELECT library_id, stage, subject_id, min(subject_kind), max(COALESCE(finished_at, created_at))
  FROM processing_jobs
 WHERE state = 'done' AND library_id IS NOT NULL AND subject_id IS NOT NULL
 GROUP BY library_id, stage, subject_id;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_live_idx
    ON processing_jobs (stage, state)
    WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_done_finished_idx
    ON processing_jobs (stage, finished_at)
    WHERE state = 'done';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_awaiting_review_idx
    ON processing_jobs (stage)
    WHERE state = 'done' AND outcome = 'awaiting_review';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_awaiting_review_idx;
DROP INDEX IF EXISTS processing_jobs_done_finished_idx;
DROP INDEX IF EXISTS processing_jobs_live_idx;
DROP TRIGGER IF EXISTS processing_done_subjects_gone_trg;
DROP TRIGGER IF EXISTS processing_done_subjects_left_trg;
DROP TRIGGER IF EXISTS processing_done_subjects_upd_trg;
DROP TRIGGER IF EXISTS processing_done_subjects_ins_trg;
DROP TABLE IF EXISTS processing_done_subjects;
-- +goose StatementEnd
