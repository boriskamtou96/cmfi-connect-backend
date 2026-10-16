-- The daily goal a member sets on an item (a standard one or their own): a
-- quantity, a duration, or both. Kept on the account so it follows them from
-- one phone to another.
CREATE TABLE IF NOT EXISTS activity_goals (
    user_id          BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    activity_type_id BIGINT NOT NULL REFERENCES activity_types (id) ON DELETE CASCADE,
    quantity         INTEGER CHECK (quantity > 0),
    minutes          INTEGER CHECK (minutes > 0),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, activity_type_id),
    -- no goal is no row
    CONSTRAINT chk_activity_goals_value CHECK (quantity IS NOT NULL OR minutes IS NOT NULL)
);
