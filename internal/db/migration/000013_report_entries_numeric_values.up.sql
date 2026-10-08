-- Numeric values so that reports can be summed: quantity for counted items,
-- duration_minutes for timed ones (see activity_types.tracks_quantity / tracks_duration).
ALTER TABLE report_entries DROP COLUMN IF EXISTS val;
ALTER TABLE report_entries DROP COLUMN IF EXISTS time_pass;
ALTER TABLE report_entries ADD COLUMN IF NOT EXISTS quantity INT CHECK (quantity >= 0);
ALTER TABLE report_entries ADD COLUMN IF NOT EXISTS duration_minutes INT CHECK (duration_minutes >= 0);
ALTER TABLE report_entries
    ADD CONSTRAINT chk_report_entries_value CHECK (quantity IS NOT NULL OR duration_minutes IS NOT NULL);
CREATE INDEX IF NOT EXISTS idx_report_entries_activity_type ON report_entries (activity_type_id);
