-- A password given by an admin (a forgotten password, without SMS) is
-- changed at the next sign-in. An account made with Google or Apple has no
-- password of its own until the member chooses one.
ALTER TABLE users
    ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN has_password BOOLEAN NOT NULL DEFAULT true;

-- The Google or Apple accounts a member signs in with, through Firebase
-- Authentication: subject is the Firebase user.
CREATE TABLE user_identities (
    provider   VARCHAR(20)  NOT NULL CHECK (provider IN ('google', 'apple')),
    subject    VARCHAR(128) NOT NULL,
    user_id    BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email      VARCHAR(320) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject)
);

CREATE INDEX idx_user_identities_user ON user_identities(user_id);
