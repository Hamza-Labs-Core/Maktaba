-- contract: the previous release reads plans by their old names (pro, family); after this, an account it sees as basic is a plan it doesn't know. Paid plans aren't on before the launch gate, so only admin-granted accounts move.
-- +goose Up
-- Story 50.19 §7: the plans are free, basic and pro. The old pro ($5) is basic and the old family
-- ($10) is pro. The CHECKs are widened to take basic beside the old names, then pro becomes basic
-- before family becomes pro (the names overlap, so the order matters and no row changes twice).
-- A later release drops family from the CHECKs.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_plan_check;
ALTER TABLE users ADD CONSTRAINT users_plan_check CHECK (plan IN ('free','basic','pro','family'));
ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_plan_check;
ALTER TABLE servers ADD CONSTRAINT servers_plan_check CHECK (plan IN ('free','basic','pro','family'));
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_plan_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_plan_check CHECK (plan IN ('free','basic','pro','family'));

UPDATE users SET plan = 'basic' WHERE plan = 'pro';
UPDATE users SET plan = 'pro' WHERE plan = 'family';
UPDATE servers SET plan = 'basic' WHERE plan = 'pro';
UPDATE servers SET plan = 'pro' WHERE plan = 'family';
UPDATE subscriptions SET plan = 'basic' WHERE plan = 'pro';
UPDATE subscriptions SET plan = 'pro' WHERE plan = 'family';

-- +goose Down
UPDATE subscriptions SET plan = 'family' WHERE plan = 'pro';
UPDATE subscriptions SET plan = 'pro' WHERE plan = 'basic';
UPDATE servers SET plan = 'family' WHERE plan = 'pro';
UPDATE servers SET plan = 'pro' WHERE plan = 'basic';
UPDATE users SET plan = 'family' WHERE plan = 'pro';
UPDATE users SET plan = 'pro' WHERE plan = 'basic';
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_plan_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_plan_check CHECK (plan IN ('free','pro','family'));
ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_plan_check;
ALTER TABLE servers ADD CONSTRAINT servers_plan_check CHECK (plan IN ('free','pro','family'));
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_plan_check;
ALTER TABLE users ADD CONSTRAINT users_plan_check CHECK (plan IN ('free','pro','family'));
