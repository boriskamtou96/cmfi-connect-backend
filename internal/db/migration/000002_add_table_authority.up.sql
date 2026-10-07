CREATE TABLE IF NOT EXISTS authorities(
    id BIGSERIAL PRIMARY KEY,
    first_name VARCHAR NOT NULL,
    last_name VARCHAR,
    phone_number VARCHAR NOT NULL,
    email VARCHAR UNIQUE,
    is_disciple_maker BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT (now())
);