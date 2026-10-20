-- A report sent in the app by a member (the disciple) to one of their
-- authorities who is a member too, for a period (a week, two, a month): the
-- authority finds it in « Comptes rendus reçus », reads the period's days
-- (GET /v1/disciples/:id/reports) and may answer with a word of
-- encouragement. Sending the same period again to the same authority makes
-- it new again (unread).
CREATE TABLE report_sendings (
    id BIGSERIAL PRIMARY KEY,
    -- the link chosen by the disciple, accepted by the authority
    authority_id BIGINT NOT NULL REFERENCES authorities(id) ON DELETE CASCADE,
    disciple_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    receiver_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_from DATE NOT NULL,
    period_to DATE NOT NULL CHECK (period_to >= period_from),
    sent_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at TIMESTAMPTZ,
    -- the authority's answer ('' before it)
    reply TEXT NOT NULL DEFAULT '' CHECK (char_length(reply) <= 500),
    replied_at TIMESTAMPTZ,
    UNIQUE (authority_id, period_from, period_to)
);

CREATE INDEX report_sendings_receiver_idx ON report_sendings (receiver_id, sent_at DESC);
CREATE INDEX report_sendings_disciple_idx ON report_sendings (disciple_id, sent_at DESC);
