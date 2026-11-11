DROP TABLE IF EXISTS user_identities;

ALTER TABLE users
    DROP COLUMN IF EXISTS has_password,
    DROP COLUMN IF EXISTS must_change_password;
