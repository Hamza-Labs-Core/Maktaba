-- +goose Up
-- +goose StatementBegin
--
-- Slot 0063 (gap-closure Wave 3 / Epic 16 Story 16.2, HLB-289) —
-- correct the licenses.tier domain from the legacy free/premium model
-- to the spec's free/home/pro model.
--
-- Why this exists
-- ----------------
-- Slot 0056 shipped CHECK (tier IN ('free','premium')). Every Epic-16
-- story and the entitlement code now model three tiers — free / home /
-- pro — with distinct seat caps and feature matrices (the verifier
-- rejects the literal "premium" string). The 0056 CHECK would reject a
-- legitimately-applied home/pro license row, so the domain must be
-- widened. 0056 is already merged, so this is a forward-only follow-up
-- slot rather than an edit to 0056.
--
-- There is no in-flight data to migrate: no Go code wrote a 'premium'
-- row on the integration branch before this slot (the persistent store
-- and license endpoint were unreachable until Wave 3), so widening the
-- domain is a pure forward correction. The defensive backfill below is
-- still applied so any stray legacy row maps premium -> pro (premium's
-- feature set is pro's).
--
-- Idempotent: the constraint is dropped IF EXISTS and re-added only
-- when absent (guarded via pg_constraint), so re-application is a
-- no-op. This file uses goose's StatementBegin / StatementEnd markers
-- around each DDL statement.
--
UPDATE licenses SET tier = 'pro' WHERE tier = 'premium';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE licenses DROP CONSTRAINT IF EXISTS licenses_tier_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'licenses_tier_check'
          AND conrelid = 'licenses'::regclass
    ) THEN
        ALTER TABLE licenses
            ADD CONSTRAINT licenses_tier_check
            CHECK (tier IN ('free', 'home', 'pro'));
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
--
-- Restore the slot-0056 free/premium domain. Down migrations are
-- dev-only; map home/pro back to premium so the narrower CHECK admits
-- the rows (lossy by construction — the three-tier distinction cannot
-- be reconstructed, which is acceptable for a dev-only down).
--
UPDATE licenses SET tier = 'premium' WHERE tier IN ('home', 'pro');
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE licenses DROP CONSTRAINT IF EXISTS licenses_tier_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'licenses_tier_check'
          AND conrelid = 'licenses'::regclass
    ) THEN
        ALTER TABLE licenses
            ADD CONSTRAINT licenses_tier_check
            CHECK (tier IN ('free', 'premium'));
    END IF;
END
$$;
-- +goose StatementEnd
