-- The reminders a disciple maker sends to a disciple who has not filled their
-- report for a while: one push each, at most one a day per disciple (checked
-- from this table).
CREATE TABLE IF NOT EXISTS disciple_nudges (
    id BIGSERIAL PRIMARY KEY,
    authority_id BIGINT NOT NULL REFERENCES authorities(id) ON DELETE CASCADE,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_disciple_nudges_authority ON disciple_nudges (authority_id, sent_at DESC);
