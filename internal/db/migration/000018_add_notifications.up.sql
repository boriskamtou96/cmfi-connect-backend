-- Notifications: the phones that receive push notifications, and the
-- announcements the admins write (title, text, images and PDF) for all the
-- members or some of them (country, city, church, assembly of the profile).

-- A phone of a member, by its Firebase Cloud Messaging token. A token moves
-- to the member signed in last on that phone.
CREATE TABLE devices (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT NOT NULL UNIQUE,
    platform TEXT NOT NULL CHECK (platform IN ('android', 'ios')),
    language TEXT NOT NULL DEFAULT 'fr',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX devices_user_id_idx ON devices (user_id);

-- An announcement written by an admin. [audience] keeps the filters chosen
-- ({} = every member); the recipients are fixed when it is sent.
CREATE TABLE announcements (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    audience JSONB NOT NULL DEFAULT '{}',
    author_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    recipients_count INT NOT NULL DEFAULT 0,
    -- the phones Firebase accepted the push for
    pushed_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The images and PDF of an announcement, in their order.
CREATE TABLE announcement_attachments (
    announcement_id BIGINT NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    position INT NOT NULL,
    media_id BIGINT NOT NULL REFERENCES media(id),
    name TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (announcement_id, position)
);

-- Who received an announcement, and when they read it.
CREATE TABLE announcement_recipients (
    announcement_id BIGINT NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at TIMESTAMPTZ,
    PRIMARY KEY (announcement_id, user_id)
);

CREATE INDEX announcement_recipients_user_idx ON announcement_recipients (user_id, read_at);
