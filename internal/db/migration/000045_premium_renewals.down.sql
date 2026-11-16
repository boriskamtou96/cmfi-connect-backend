DROP TABLE IF EXISTS premium_renewals;
ALTER TABLE payments
    DROP COLUMN IF EXISTS renewal,
    DROP COLUMN IF EXISTS renew;
