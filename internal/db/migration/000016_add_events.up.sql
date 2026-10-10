-- Images sent by the admins: posters, pictures of the news, photo galleries.
-- The file lives in the media storage under [key]; [color] is its dominant
-- color, shown while the image loads.
CREATE TABLE media (
    id BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    content_type TEXT NOT NULL,
    width INT NOT NULL CHECK (width > 0),
    height INT NOT NULL CHECK (height > 0),
    color TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL,
    uploaded_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- An event held every year (Conférence de Pâques...): its editions are events.
CREATE TABLE event_series (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE events (
    id BIGSERIAL PRIMARY KEY,
    series_id BIGINT REFERENCES event_series(id) ON DELETE SET NULL,
    title TEXT NOT NULL CHECK (title <> ''),
    theme TEXT NOT NULL DEFAULT '',
    theme_verse TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    -- the times are shown in the time zone of the place (IANA name)
    time_zone TEXT NOT NULL DEFAULT 'Africa/Douala',
    venue TEXT NOT NULL DEFAULT '',
    address TEXT NOT NULL DEFAULT '',
    city TEXT NOT NULL DEFAULT '',
    country TEXT NOT NULL DEFAULT '',
    maps_url TEXT NOT NULL DEFAULT '',
    contact_name TEXT NOT NULL DEFAULT '',
    contact_phone TEXT NOT NULL DEFAULT '',
    poster_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    registration_open BOOLEAN NOT NULL DEFAULT TRUE,
    registration_deadline TIMESTAMPTZ,
    -- people, all registrations together; null: no limit
    capacity INT CHECK (capacity IS NULL OR capacity > 0),
    asks_lodging BOOLEAN NOT NULL DEFAULT FALSE,
    asks_departure_city BOOLEAN NOT NULL DEFAULT FALSE,
    fee_note TEXT NOT NULL DEFAULT '',
    -- who sees it: the whole community for now (local churches will come later)
    scope TEXT NOT NULL DEFAULT 'community' CHECK (scope IN ('community')),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'cancelled')),
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at >= starts_at)
);

CREATE INDEX idx_events_visible ON events (starts_at) WHERE status <> 'draft';
CREATE INDEX idx_events_series ON events (series_id) WHERE series_id IS NOT NULL;

-- The program: one row per session (worship, teaching, prayer, meal...).
CREATE TABLE event_sessions (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ,
    title TEXT NOT NULL CHECK (title <> ''),
    speaker TEXT NOT NULL DEFAULT '',
    place TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    -- hymns sung: [{"book": "rendons-ministere-1", "number": 12}]
    hymns JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(hymns) = 'array'),
    CHECK (ends_at IS NULL OR ends_at >= starts_at)
);

CREATE INDEX idx_event_sessions_event ON event_sessions (event_id, starts_at);

CREATE TABLE event_registrations (
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    people INT NOT NULL DEFAULT 1 CHECK (people BETWEEN 1 AND 50),
    needs_lodging BOOLEAN NOT NULL DEFAULT FALSE,
    departure_city TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, user_id)
);

CREATE INDEX idx_event_registrations_user ON event_registrations (user_id);

-- The news of an event: "the hall changes", "the photos are online"...
CREATE TABLE event_posts (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    body TEXT NOT NULL CHECK (body <> ''),
    image_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    pinned BOOLEAN NOT NULL DEFAULT FALSE,
    author_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_event_posts_event ON event_posts (event_id, created_at DESC);

CREATE TABLE event_photos (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    caption TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_event_photos_event ON event_photos (event_id, id);
