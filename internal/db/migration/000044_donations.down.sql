-- the donations made stay in the accounts (NOT VALID: the rows already
-- there are not checked); new ones are refused again
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_purpose_check;
ALTER TABLE payments
    ADD CONSTRAINT payments_purpose_check
    CHECK (purpose IN ('event', 'premium')) NOT VALID;
