DROP INDEX IF EXISTS idx_report_entries_activity_type;
ALTER TABLE report_entries DROP CONSTRAINT IF EXISTS chk_report_entries_value;
ALTER TABLE report_entries DROP COLUMN IF EXISTS duration_minutes;
ALTER TABLE report_entries DROP COLUMN IF EXISTS quantity;
ALTER TABLE report_entries ADD COLUMN IF NOT EXISTS val TEXT NOT NULL DEFAULT '';
ALTER TABLE report_entries ALTER COLUMN val DROP DEFAULT;
ALTER TABLE report_entries ADD COLUMN IF NOT EXISTS time_pass VARCHAR(30);
