ALTER TABLE payments DROP COLUMN payout_id;
DROP TABLE payouts;
ALTER TABLE events
    DROP COLUMN payout_confirmed_at,
    DROP COLUMN payout_holder,
    DROP COLUMN payout_provider,
    DROP COLUMN payout_phone;
