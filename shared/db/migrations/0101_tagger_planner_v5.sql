-- +goose Up
-- +goose StatementBegin
--
-- Slot 0101 (plan 06-11 section 17.7) - channel relevance, second pass: the tagger (v5:
-- an episode in its show's own chapter playlist «كتاب الصيام» takes the chapter's topic;
-- a season label «رمضان 1445» in a name or playlist is when it aired, not its topic) and
-- the planner (v5: on a topic channel its topic's shows and specialists come first; a
-- recited Taraweeh night is no lesson channel's special run). The version sweep re-tags
-- and re-plans once.
--
UPDATE job_stages SET version = 5 WHERE stage = 'tag';
UPDATE job_stages SET version = 5 WHERE stage = 'plan_channels';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 4 WHERE stage = 'tag';
UPDATE job_stages SET version = 4 WHERE stage = 'plan_channels';
-- +goose StatementEnd
