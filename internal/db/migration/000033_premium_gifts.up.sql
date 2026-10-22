-- Premium offered by the admins, 1 to 3 months, to one member or to a group
-- (the members of a country, a city, a church, an assembly, or everyone).
-- Each member's gift is given in RevenueCat (a promotional entitlement),
-- after their current Premium.
CREATE TABLE premium_offers (
    id BIGSERIAL PRIMARY KEY,
    months INT NOT NULL CHECK (months BETWEEN 1 AND 3),
    -- {"username": "…"} or the places of the group ({"city": "Yaoundé"})
    audience JSONB NOT NULL,
    -- why, for the admins
    note TEXT NOT NULL DEFAULT '',
    notify BOOLEAN NOT NULL DEFAULT TRUE,
    members_count INT NOT NULL DEFAULT 0,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);

CREATE TABLE premium_gifts (
    id BIGSERIAL PRIMARY KEY,
    offer_id BIGINT NOT NULL REFERENCES premium_offers(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    months INT NOT NULL,
    -- pending (to give), given, failed (RevenueCat refused it 5 times),
    -- revoked (the offer was taken back)
    status TEXT NOT NULL DEFAULT 'pending',
    -- the end of the Premium it gave; null for a member with Premium for life
    until TIMESTAMPTZ,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (offer_id, user_id)
);

CREATE INDEX idx_premium_gifts_user ON premium_gifts (user_id);
CREATE INDEX idx_premium_gifts_pending ON premium_gifts (updated_at) WHERE status = 'pending';
