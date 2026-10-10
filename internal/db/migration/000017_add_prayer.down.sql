DROP TABLE IF EXISTS importunity_members;
DROP TABLE IF EXISTS importunities;
DROP TABLE IF EXISTS prayer_retreats;
DROP TABLE IF EXISTS prayer_people;
DROP TABLE IF EXISTS prayer_topic_prayers;
DROP TABLE IF EXISTS prayer_topic_points;
DROP TABLE IF EXISTS prayer_topics;

-- the documents can't stay: an image has a size
DELETE FROM media WHERE width = 0 OR height = 0;
ALTER TABLE media
    DROP CONSTRAINT media_width_check,
    DROP CONSTRAINT media_height_check,
    ADD CONSTRAINT media_width_check CHECK (width > 0),
    ADD CONSTRAINT media_height_check CHECK (height > 0);
