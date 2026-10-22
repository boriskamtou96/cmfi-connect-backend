-- The money of an event goes to its own mobile money account (an Orange
-- Money or MTN number), sent from the KPAY wallet when an admin asks
-- (« Reverser »). The account is confirmed by a test of 100 FCFA: only a
-- confirmed account receives the money.
ALTER TABLE events
    ADD COLUMN payout_phone TEXT NOT NULL DEFAULT '',
    ADD COLUMN payout_provider TEXT NOT NULL DEFAULT '',
    ADD COLUMN payout_holder TEXT NOT NULL DEFAULT '',
    ADD COLUMN payout_confirmed_at TIMESTAMPTZ;

-- One sending from the wallet to an event's account. The id is the
-- externalId given to KPAY.
CREATE TABLE payouts (
    id UUID PRIMARY KEY,
    event_id BIGINT REFERENCES events(id) ON DELETE SET NULL,
    -- test (100 FCFA, to confirm the account) or transfer (the money)
    kind TEXT NOT NULL CHECK (kind IN ('test', 'transfer')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL,
    phone_number TEXT NOT NULL,
    provider TEXT NOT NULL,
    holder TEXT NOT NULL DEFAULT '',
    -- initiated (sent, answer unknown), pending, completed, failed
    status TEXT NOT NULL DEFAULT 'initiated',
    failure_code TEXT NOT NULL DEFAULT '',
    provider_payout_id TEXT NOT NULL DEFAULT '',
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_payouts_event ON payouts (event_id, created_at DESC);

-- the sending that carried a payment's money to its event's account
ALTER TABLE payments
    ADD COLUMN payout_id UUID REFERENCES payouts(id) ON DELETE SET NULL;
