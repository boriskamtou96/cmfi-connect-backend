CREATE UNIQUE INDEX idx_unique_user_disciple_maker
ON authorities (user_id)
WHERE is_disciple_maker = true;