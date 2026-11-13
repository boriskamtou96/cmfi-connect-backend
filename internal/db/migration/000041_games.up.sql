-- The games (the tab in the middle of the app): courses ("Notre ministère",
-- "La Bible") made of levels, each level of short lessons of questions. A
-- member plays a lesson, the server counts the points; the leaderboard is the
-- sum of the plays. Translated texts are JSONB: {"fr": "…", "en": "…"}, "fr"
-- always there.
CREATE TABLE IF NOT EXISTS game_courses (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(40) NOT NULL UNIQUE CHECK (code ~ '^[a-z][a-z0-9_-]*$'),
    position INT NOT NULL DEFAULT 1,
    title JSONB NOT NULL,
    -- NULL: a draft, hidden from the members
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS game_levels (
    id BIGSERIAL PRIMARY KEY,
    course_id BIGINT NOT NULL REFERENCES game_courses(id) ON DELETE CASCADE,
    position INT NOT NULL,
    title JSONB NOT NULL,
    description JSONB NOT NULL DEFAULT '{}',
    -- the color of the level's banner on the path
    color VARCHAR(7) NOT NULL DEFAULT '#0B3B70' CHECK (color ~ '^#[0-9A-Fa-f]{6}$'),
    published_at TIMESTAMPTZ,
    UNIQUE (course_id, position)
);

CREATE TABLE IF NOT EXISTS game_lessons (
    id BIGSERIAL PRIMARY KEY,
    level_id BIGINT NOT NULL REFERENCES game_levels(id) ON DELETE CASCADE,
    position INT NOT NULL,
    title JSONB NOT NULL,
    published_at TIMESTAMPTZ,
    UNIQUE (level_id, position)
);

CREATE TABLE IF NOT EXISTS game_questions (
    id BIGSERIAL PRIMARY KEY,
    lesson_id BIGINT NOT NULL REFERENCES game_lessons(id) ON DELETE CASCADE,
    position INT NOT NULL,
    kind VARCHAR(20) NOT NULL CHECK (kind IN ('choice', 'true_false', 'fill_verse')),
    prompt JSONB NOT NULL,
    -- [{"id": "a", "text": {"fr": "…"}}]
    choices JSONB NOT NULL,
    -- the id of the right choice
    answer VARCHAR(20) NOT NULL,
    explanation JSONB NOT NULL DEFAULT '{}',
    -- where the answer comes from: a Bible reference ("GEN.1.3") or a page
    -- of Notre ministère ("ministry:founder")
    source VARCHAR(120) NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE INDEX IF NOT EXISTS idx_game_questions_lesson ON game_questions (lesson_id, position);

-- What a member did of a lesson: its best play.
CREATE TABLE IF NOT EXISTS game_lesson_results (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    lesson_id BIGINT NOT NULL REFERENCES game_lessons(id) ON DELETE CASCADE,
    best_stars SMALLINT NOT NULL CHECK (best_stars BETWEEN 1 AND 3),
    best_first_try SMALLINT NOT NULL,
    plays INT NOT NULL DEFAULT 1,
    first_completed_at TIMESTAMPTZ NOT NULL,
    last_played_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, lesson_id)
);

-- One line per play (a lesson, a lesson played again, the challenge of the
-- day). client_id comes from the phone: the same play sent twice counts once.
CREATE TABLE IF NOT EXISTS game_plays (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- NULL for the challenge of the day, or once the lesson is gone
    lesson_id BIGINT REFERENCES game_lessons(id) ON DELETE SET NULL,
    kind VARCHAR(10) NOT NULL CHECK (kind IN ('lesson', 'replay', 'daily')),
    client_id VARCHAR(64) NOT NULL CHECK (btrim(client_id) <> ''),
    first_try SMALLINT NOT NULL DEFAULT 0,
    total SMALLINT NOT NULL DEFAULT 0,
    stars SMALLINT NOT NULL DEFAULT 0,
    points INT NOT NULL CHECK (points >= 0),
    combo_bonus INT NOT NULL DEFAULT 0,
    -- this play took the member to their next level
    level_up BOOLEAN NOT NULL DEFAULT FALSE,
    played_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, client_id)
);
CREATE INDEX IF NOT EXISTS idx_game_plays_played ON game_plays (played_at, user_id);

-- A player: their total, their streak of days (in Douala time, one day of
-- grace a week) and their choices.
CREATE TABLE IF NOT EXISTS game_players (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    total_points INT NOT NULL DEFAULT 0,
    -- NULL until the member chose to appear in the leaderboard or not
    in_leaderboard BOOLEAN,
    streak_days INT NOT NULL DEFAULT 0,
    last_played_on DATE,
    -- the missed day the grace of its week covered
    grace_used_on DATE,
    daily_goal INT NOT NULL DEFAULT 50 CHECK (daily_goal IN (30, 50, 100)),
    -- the day the challenge of the day was last done
    daily_done_on DATE
);
