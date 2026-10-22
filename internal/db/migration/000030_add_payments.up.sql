-- Payments by mobile money (pawaPay): an event's registration and the
-- Premium bought on the website.

-- the price of an event, per person (XAF has no cents: whole units); null for
-- a free event
ALTER TABLE events
    ADD COLUMN price_amount BIGINT CHECK (price_amount > 0),
    ADD COLUMN price_currency TEXT NOT NULL DEFAULT 'XAF';

-- One payment asked of a member. The id is the depositId given to pawaPay
-- (and refund_id the refundId of its refund). The rows stay when the member
-- or the event goes: they are the accounts.
CREATE TABLE payments (
    id UUID PRIMARY KEY,
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    -- event or premium
    purpose TEXT NOT NULL CHECK (purpose IN ('event', 'premium')),
    event_id BIGINT REFERENCES events(id) ON DELETE SET NULL,
    -- the registration paid for
    people INT NOT NULL DEFAULT 1,
    needs_lodging BOOLEAN NOT NULL DEFAULT FALSE,
    departure_city TEXT NOT NULL DEFAULT '',
    -- monthly, six_month, yearly or lifetime
    premium_plan TEXT NOT NULL DEFAULT '',
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL,
    -- the mobile money account (MSISDN) and its operator (MTN_MOMO_CMR…)
    phone_number TEXT NOT NULL,
    provider TEXT NOT NULL,
    -- initiated (sent, answer unknown), accepted (the member confirms with
    -- their PIN), completed, failed, refund_pending, refunded, refund_failed
    status TEXT NOT NULL DEFAULT 'initiated',
    failure_code TEXT NOT NULL DEFAULT '',
    provider_transaction_id TEXT NOT NULL DEFAULT '',
    -- the completed payment did its work (registration made, Premium given)
    settled_at TIMESTAMPTZ,
    -- the end of the Premium it gave; null for lifetime
    premium_until TIMESTAMPTZ,
    refund_id UUID UNIQUE,
    -- admin, or event_unavailable (full or closed when the money arrived)
    refund_reason TEXT NOT NULL DEFAULT '',
    refunded_at TIMESTAMPTZ,
    -- the last time pawaPay was asked about it
    checked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_payments_event ON payments (event_id, created_at DESC);
CREATE INDEX idx_payments_user ON payments (user_id, created_at DESC);
-- what the reconciler looks at
CREATE INDEX idx_payments_open ON payments (updated_at)
    WHERE status IN ('initiated', 'accepted', 'refund_pending')
       OR (status = 'completed' AND settled_at IS NULL);

-- the payment behind a registration to a paid event
ALTER TABLE event_registrations
    ADD COLUMN payment_id UUID REFERENCES payments(id) ON DELETE SET NULL;
