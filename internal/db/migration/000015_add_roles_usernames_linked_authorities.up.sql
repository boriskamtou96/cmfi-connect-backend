-- Roles: who may manage the content shared by everyone.
-- Pseudos: unique, lowercase, chosen by each member (the existing accounts get one
-- made from their name, which they can change).
ALTER TABLE users
    ADD COLUMN role VARCHAR(20) NOT NULL DEFAULT 'user'
        CHECK (role IN ('user', 'admin', 'superadmin')),
    ADD COLUMN username VARCHAR(30);

-- boris.kamtou, then boris.kamtou2, boris.kamtou3... when it is taken
DO $$
DECLARE
    member RECORD;
    base TEXT;
    candidate TEXT;
    n INT;
BEGIN
    FOR member IN SELECT id, first_name, last_name FROM users ORDER BY id LOOP
        base := lower(translate(
            concat_ws('.', nullif(trim(member.first_name), ''), nullif(trim(member.last_name), '')),
            'àâäáãåçéèêëíìîïñóòôöõúùûüýÿÀÂÄÁÃÅÇÉÈÊËÍÌÎÏÑÓÒÔÖÕÚÙÛÜÝ',
            'aaaaaaceeeeiiiinooooouuuuyyAAAAAACEEEEIIIINOOOOOUUUUY'
        ));
        base := regexp_replace(base, '[^a-z0-9.]+', '', 'g');
        base := regexp_replace(base, '\.{2,}', '.', 'g');
        base := trim(BOTH '.' FROM left(trim(BOTH '.' FROM base), 24));
        IF length(base) < 3 THEN
            base := 'membre';
        END IF;

        candidate := base;
        n := 1;
        WHILE EXISTS (SELECT 1 FROM users WHERE username = candidate) LOOP
            n := n + 1;
            candidate := base || n;
        END LOOP;
        UPDATE users SET username = candidate WHERE id = member.id;
    END LOOP;
END $$;

ALTER TABLE users
    ALTER COLUMN username SET NOT NULL,
    ADD CONSTRAINT users_username_key UNIQUE (username),
    -- 3 to 30 characters: a-z, 0-9, "." and "_", starting and ending with a letter or a digit
    ADD CONSTRAINT users_username_format
        CHECK (username ~ '^[a-z0-9][a-z0-9._]{1,28}[a-z0-9]$');

-- An authority can be a member of the app, found by pseudo. The link waits
-- until that member accepts; their contact comes from their account.
ALTER TABLE authorities
    ADD COLUMN linked_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'accepted'
        CHECK (status IN ('pending', 'accepted', 'declined')),
    ALTER COLUMN phone_number DROP NOT NULL,
    -- the same person can accompany several members
    DROP CONSTRAINT IF EXISTS authorities_email_key;

CREATE UNIQUE INDEX idx_authorities_user_linked
    ON authorities (user_id, linked_user_id)
    WHERE linked_user_id IS NOT NULL;

CREATE INDEX idx_authorities_linked_user
    ON authorities (linked_user_id)
    WHERE linked_user_id IS NOT NULL;

-- a declined request doesn't keep the disciple maker's place
DROP INDEX IF EXISTS idx_unique_user_disciple_maker;
CREATE UNIQUE INDEX idx_unique_user_disciple_maker
    ON authorities (user_id)
    WHERE is_disciple_maker AND status <> 'declined';
