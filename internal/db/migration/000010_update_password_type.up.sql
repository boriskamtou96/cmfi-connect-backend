ALTER TABLE users
ALTER COLUMN hash_password TYPE BYTEA USING hash_password::bytea;