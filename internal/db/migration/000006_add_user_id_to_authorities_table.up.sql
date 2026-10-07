ALTER TABLE authorities
ADD COLUMN user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_authorities_user_id ON authorities(user_id);