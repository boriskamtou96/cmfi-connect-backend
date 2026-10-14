ALTER TABLE daily_reports
    DROP COLUMN IF EXISTS goal_percent;
ALTER TABLE authorities
    DROP COLUMN IF EXISTS shares_regularity;
