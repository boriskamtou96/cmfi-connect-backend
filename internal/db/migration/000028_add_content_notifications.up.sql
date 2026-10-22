-- The new teachings, documents, events and prayer topics of the community
-- are announced to the members by a push, once: the first time they are
-- published with « Prévenir les membres » (the default). notified_at is
-- when it was sent; it is never reset, so a fix or a second click sends
-- nothing again.
ALTER TABLE teachings ADD COLUMN notified_at TIMESTAMPTZ;
ALTER TABLE documents ADD COLUMN notified_at TIMESTAMPTZ;
ALTER TABLE events ADD COLUMN notified_at TIMESTAMPTZ;
ALTER TABLE prayer_topics ADD COLUMN notified_at TIMESTAMPTZ;

-- what was already published before is not announced as new
UPDATE teachings SET notified_at = now() WHERE published;
UPDATE documents SET notified_at = now() WHERE published;
UPDATE events SET notified_at = now() WHERE status <> 'draft';
UPDATE prayer_topics SET notified_at = now() WHERE status = 'published';
