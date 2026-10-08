CREATE TABLE IF NOT EXISTS activity_types (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    code VARCHAR(20) NOT NULL, -- LB, PS...
    label TEXT NOT NULL, -- ex: Prayer alone
    tracks_quantity BOOLEAN NOT NULL DEFAULT FALSE,
    quantity_unit TEXT,
    tracks_duration BOOLEAN NOT NULL DEFAULT FALSE,
    position INT NOT NULL DEFAULT 0,
    archived_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_activity_types_owner_code UNIQUE NULLS NOT DISTINCT (user_id, code),
    CONSTRAINT chk_activity_types_measure CHECK (tracks_quantity OR tracks_duration),
    CONSTRAINT chk_activity_types_unit CHECK (quantity_unit IS NULL OR tracks_quantity)
);

CREATE TABLE IF NOT EXISTS daily_reports (
   id          BIGSERIAL PRIMARY KEY,
   user_id     BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
   report_date DATE NOT NULL,
   note        TEXT,
   created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
   updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
   CONSTRAINT uq_daily_reports_user_date UNIQUE (user_id, report_date)
);

CREATE TABLE IF NOT EXISTS report_entries (
    report_id        BIGINT NOT NULL REFERENCES daily_reports (id) ON DELETE CASCADE,
    activity_type_id BIGINT NOT NULL REFERENCES activity_types (id) ON DELETE RESTRICT,
    val         TEXT NOT NULL,
    time_pass VARCHAR(30),
    PRIMARY KEY (report_id, activity_type_id)
   -- CONSTRAINT chk_report_entries_value CHECK (val IS NOT NULL OR duration_minutes IS NOT NULL)
);