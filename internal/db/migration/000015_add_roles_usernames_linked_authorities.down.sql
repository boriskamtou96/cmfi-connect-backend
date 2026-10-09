DROP INDEX IF EXISTS idx_unique_user_disciple_maker;
CREATE UNIQUE INDEX idx_unique_user_disciple_maker
    ON authorities (user_id)
    WHERE is_disciple_maker = true;

DROP INDEX IF EXISTS idx_authorities_linked_user;
DROP INDEX IF EXISTS idx_authorities_user_linked;

-- authorities linked to a member keep the name they were saved with. The
-- UNIQUE on authorities.email is not put back: two members may share an authority.
UPDATE authorities SET phone_number = '' WHERE phone_number IS NULL;

ALTER TABLE authorities
    DROP COLUMN status,
    DROP COLUMN linked_user_id,
    ALTER COLUMN phone_number SET NOT NULL;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_username_format,
    DROP CONSTRAINT IF EXISTS users_username_key,
    DROP COLUMN username,
    DROP COLUMN role;
