-- A member shows their regularity (the days they filled in) to their disciple
-- maker: on when they choose them, they can turn it off.
ALTER TABLE authorities
    ADD COLUMN shares_regularity BOOLEAN NOT NULL DEFAULT TRUE;

-- The share of the day's goals reached, sent by the phone; NULL without goals.
ALTER TABLE daily_reports
    ADD COLUMN goal_percent SMALLINT CHECK (goal_percent BETWEEN 0 AND 100);
