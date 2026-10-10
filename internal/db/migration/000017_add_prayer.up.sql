-- Prayer: the prayer topics of the community (published by the admins), the
-- members' own lists, the 15-minute retreats they made, and the importunity
-- topics they pray together until a number of proclamations is reached.

-- The media storage keeps documents too (the PDF of a prayer topic): they
-- have no width nor height.
ALTER TABLE media
    DROP CONSTRAINT media_width_check,
    DROP CONSTRAINT media_height_check,
    ADD CONSTRAINT media_width_check CHECK (width >= 0),
    ADD CONSTRAINT media_height_check CHECK (height >= 0);

-- A prayer topic of the whole community: a title and numbered points, for a
-- period. Often made from a PDF, kept as [document_id].
CREATE TABLE prayer_topics (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL CHECK (title <> ''),
    category TEXT NOT NULL DEFAULT 'church'
        CHECK (category IN ('church', 'nations', 'sick', 'event', 'fasting', 'other')),
    verse TEXT NOT NULL DEFAULT '',
    starts_on DATE NOT NULL,
    -- null: no end
    ends_on DATE,
    event_id BIGINT REFERENCES events(id) ON DELETE SET NULL,
    document_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    -- the name of the file as the admin sent it
    document_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published')),
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_on IS NULL OR ends_on >= starts_on)
);

CREATE INDEX idx_prayer_topics_published ON prayer_topics (starts_on) WHERE status = 'published';

CREATE TABLE prayer_topic_points (
    topic_id BIGINT NOT NULL REFERENCES prayer_topics(id) ON DELETE CASCADE,
    -- 1, 2, 3...
    position INT NOT NULL CHECK (position > 0),
    body TEXT NOT NULL CHECK (body <> ''),
    PRIMARY KEY (topic_id, position)
);

-- "I prayed for this point today". Kept by position, so an admin can fix the
-- text of a point; the apps show how many pray, never who.
CREATE TABLE prayer_topic_prayers (
    topic_id BIGINT NOT NULL REFERENCES prayer_topics(id) ON DELETE CASCADE,
    position INT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    prayed_on DATE NOT NULL,
    PRIMARY KEY (topic_id, position, user_id, prayed_on)
);

CREATE INDEX idx_prayer_topic_prayers_user ON prayer_topic_prayers (user_id, prayed_on);

-- The people a member prays for, as the retreats ask: those who have not
-- believed yet, believers, backsliders, co-workers. Only their owner sees them.
CREATE TABLE prayer_people (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    list TEXT NOT NULL CHECK (list IN ('unbelievers', 'believers', 'backsliders', 'coworkers')),
    name TEXT NOT NULL CHECK (name <> ''),
    note TEXT NOT NULL DEFAULT '',
    -- the last day a retreat prayed for them: the others come first
    last_prayed_on DATE,
    -- believed in the Lord, came back, or the prayer was answered
    answered_on DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_prayer_people_user ON prayer_people (user_id, list);

-- The 15-minute retreats a member made: morning, noon, evening, night.
CREATE TABLE prayer_retreats (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day DATE NOT NULL,
    moment TEXT NOT NULL CHECK (moment IN ('morning', 'noon', 'evening', 'night')),
    minutes INT NOT NULL DEFAULT 15 CHECK (minutes > 0 AND minutes <= 240),
    done_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, day, moment)
);

-- An importunity topic ("Lord, save Paul"): its author and the members they
-- invite count their proclamations together, until [goal] is reached.
CREATE TABLE importunities (
    id BIGSERIAL PRIMARY KEY,
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK (title <> ''),
    description TEXT NOT NULL DEFAULT '',
    -- null: no goal, the proclamations are only counted
    goal BIGINT CHECK (goal IS NULL OR goal > 0),
    -- all the proclamations, kept even when a participant leaves
    total BIGINT NOT NULL DEFAULT 0 CHECK (total >= 0),
    reached_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE importunity_members (
    importunity_id BIGINT NOT NULL REFERENCES importunities(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invited_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    -- the author joins from the start
    status TEXT NOT NULL DEFAULT 'invited' CHECK (status IN ('invited', 'joined', 'declined')),
    -- this member's proclamations: only they see it
    count BIGINT NOT NULL DEFAULT 0 CHECK (count >= 0),
    joined_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (importunity_id, user_id)
);

CREATE INDEX idx_importunity_members_user ON importunity_members (user_id);
