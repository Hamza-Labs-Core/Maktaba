-- +goose Up
-- Story 50.33 §1: a customer account is never an operator again. Staff accounts replace
-- users.role = 'service_admin'; no production data exists yet, so every row goes back to
-- 'user', and nothing reads the column any more. A later migration drops it.
UPDATE users SET role = 'user' WHERE role <> 'user';

-- +goose Down
-- the old grants are gone for good: run `maktaba-cloud admin grant` on the old code again
SELECT 1;
