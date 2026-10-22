ALTER TABLE event_registrations DROP COLUMN payment_id;
DROP TABLE payments;
ALTER TABLE events
    DROP COLUMN price_currency,
    DROP COLUMN price_amount;
