-- Our missionaries and our heroes (the pages of "Notre ministère"): written
-- by the admins, read by every member once published.
-- missionary: [country_code] where they serve (ISO, flag in the apps), [role]
-- ("Implantation d'églises"), [since_year]; hero: [years] ("1920–1998"),
-- [role] (what they did). [story] is their story, in plain text.
CREATE TABLE ministry_people (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('missionary', 'hero')),
    name TEXT NOT NULL CHECK (length(btrim(name)) > 0),
    photo_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    country_code TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT '',
    since_year INT CHECK (since_year IS NULL OR since_year BETWEEN 1900 AND 2100),
    years TEXT NOT NULL DEFAULT '',
    story TEXT NOT NULL DEFAULT '',
    position INT NOT NULL DEFAULT 0,
    published BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ministry_people_kind_position_idx ON ministry_people (kind, position);
