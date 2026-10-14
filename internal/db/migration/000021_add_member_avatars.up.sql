-- The profile photo of a member: a square JPEG of the media storage, shown
-- on their card. It goes with the account; an image deleted takes it off.
ALTER TABLE users
    ADD COLUMN avatar_media_id BIGINT REFERENCES media(id) ON DELETE SET NULL;
