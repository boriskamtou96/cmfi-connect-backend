-- A member stops another one's invitations (importunity topics): the
-- blocked member is not told, their invitations to the blocker are ignored.
CREATE TABLE member_blocks (
    blocker_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, blocked_id),
    CHECK (blocker_id <> blocked_id)
);

-- A member reports another one to the admins (an invitation to a topic),
-- handled in the back office.
CREATE TABLE member_reports (
    id BIGSERIAL PRIMARY KEY,
    reporter_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reported_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    importunity_id BIGINT REFERENCES importunities(id) ON DELETE SET NULL,
    -- the title at the time of the report: it stays if the topic is deleted
    importunity_title TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL CHECK (reason IN ('spam', 'offensive', 'other')),
    comment TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    resolved_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    CHECK (reporter_id <> reported_id)
);

CREATE INDEX member_reports_status_idx ON member_reports (status, created_at DESC);
CREATE INDEX member_reports_reported_idx ON member_reports (reported_id);
