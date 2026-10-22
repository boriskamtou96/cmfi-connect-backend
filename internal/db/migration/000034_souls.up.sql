-- Suivi des âmes: the people a member evangelised and follows, kept for
-- that member only (synchronised from their phones; nobody else reads them).
-- The id is made by the app (offline first).
CREATE TABLE souls (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    phone TEXT NOT NULL DEFAULT '',
    place TEXT NOT NULL DEFAULT '',
    met_on DATE NOT NULL,
    -- met, interested, received, followUp, church, baptized
    stage TEXT NOT NULL DEFAULT 'met',
    next_contact DATE,
    -- [{"at": "…", "text": "…"}], the newest first
    notes JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id)
);
