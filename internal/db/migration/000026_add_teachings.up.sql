-- Enseignements: the audio messages of the community (a preaching, a
-- teaching, a series), an MP3 or M4A file each, sent by the admins with the
-- speaker, a category and an optional cover. The published ones are heard by
-- every member, the newest message first.
CREATE TABLE teachings (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    -- "Pr Zacharias Tanee Fomum"; '' when not said
    speaker TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    -- "Prédications", "La vie chrétienne"…; '' when none
    category TEXT NOT NULL DEFAULT '',
    -- the day the message was given (not the day it was sent); null if unknown
    recorded_on DATE,
    -- the sound (media: an MP3 or an M4A), kept while listed
    audio_id BIGINT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    -- read by the admin site when the file is chosen; 0 when unknown
    duration_seconds INT NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    -- a square picture shown in the list and the player; null for none
    cover_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    published BOOLEAN NOT NULL DEFAULT FALSE,
    -- set the first time it is published
    published_at TIMESTAMPTZ,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX teachings_published_idx ON teachings (published, recorded_on DESC, published_at DESC);
