-- « Faire un don »: a payment with nothing in return, of an amount the
-- member chooses. It goes through the same payments as an event or Premium.
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_purpose_check;
ALTER TABLE payments
    ADD CONSTRAINT payments_purpose_check
    CHECK (purpose IN ('event', 'premium', 'donation'));
