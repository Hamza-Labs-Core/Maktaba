-- +goose Up
-- +goose StatementBegin
--
-- Slot 0100 (plan 06-11 section 17.7) - channel relevance: the tagger (v4: airing in
-- Ramadan / Dhu al-Hijjah is occasion:ramadan_season / hajj_season, not the topic; a
-- month that is a person's name «الشيخ رمضان» is not the month; a playlist holding
-- several shows is no evidence) and the planner (v4: a show joins a theme whole only when
-- its own episodes agree; theme channels without the general filler). The seeded rows
-- carry the new versions; the version sweep re-tags and re-plans once.
--
UPDATE job_stages SET version = 4 WHERE stage = 'tag';
UPDATE job_stages SET version = 4 WHERE stage = 'plan_channels';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 3 WHERE stage = 'tag';
UPDATE job_stages SET version = 3 WHERE stage = 'plan_channels';
-- +goose StatementEnd
