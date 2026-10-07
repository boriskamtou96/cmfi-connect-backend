DROP INDEX IF EXISTS idx_authorities_user_id;

ALTER TABLE authorities
DROP COLUMN IF EXISTS user_id;