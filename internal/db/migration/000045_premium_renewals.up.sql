-- Premium renewed by itself (16/11/2026). Mobile money can't be taken
-- without the member's PIN: the day before the end, the server sends the
-- payment request of the same plan to the same number, the member types the
-- PIN. Chosen when paying (« Renouveler automatiquement »), stopped at any
-- time.

-- renew: the member asked for it with this payment (kept once it is paid);
-- renewal: this payment is a request the server sent by itself.
ALTER TABLE payments
    ADD COLUMN renew BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN renewal BOOLEAN NOT NULL DEFAULT false;

-- One per member: what to ask, to which number.
CREATE TABLE premium_renewals (
    user_id BIGINT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    plan TEXT NOT NULL CHECK (plan IN ('monthly', 'six_month', 'yearly')),
    phone_number TEXT NOT NULL,
    provider TEXT NOT NULL,
    -- the end of Premium the requests are for: a new end starts them again
    period_end TIMESTAMPTZ NOT NULL,
    -- the requests sent for this end (3 at most), the last one
    attempts INT NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMPTZ,
    last_payment_id UUID REFERENCES payments (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
