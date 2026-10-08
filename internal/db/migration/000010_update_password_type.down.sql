ALTER TABLE users
ALTER COLUMN hash_password TYPE VARCHAR USING hash_password::text;