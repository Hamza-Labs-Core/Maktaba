-- +goose Up
-- +goose StatementBegin
--
-- Slot 0113 (plan 06-11 section 3.2, plan 03-10) - the tagger after the merge of both
-- lines: v7 reads transcripts as evidence (slot 0100's tagger v4) and tags each episode by
-- its own title and chapter playlist, strips the channel's branding and keeps a season
-- label the airing season (slot 0109's tagger v6). The version sweep re-tags once.
--
UPDATE job_stages SET version = 7 WHERE stage = 'tag';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 6 WHERE stage = 'tag';
-- +goose StatementEnd
