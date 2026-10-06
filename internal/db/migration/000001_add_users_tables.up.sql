CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    first_name VARCHAR(255) NOT NULL,
    last_name VARCHAR(255),
    hash_password VARCHAR NOT NULL,
    created_at TIMESTAMPZ NOT NULL DEFAULT (now())
);