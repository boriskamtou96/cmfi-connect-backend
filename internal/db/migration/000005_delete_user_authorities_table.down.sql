CREATE TABLE IF NOT EXISTS user_authorities (
                                                user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    authority_id BIGINT NOT NULL REFERENCES authorities(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (user_id, authority_id)
    );