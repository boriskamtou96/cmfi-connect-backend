-- What the members miss: per question, the first answers given during a
-- first play of its lesson, and how many were wrong. The back-office shows
-- the questions missed the most (often badly asked: to rewrite).
CREATE TABLE IF NOT EXISTS game_question_stats (
    question_id BIGINT PRIMARY KEY REFERENCES game_questions(id) ON DELETE CASCADE,
    asked INT NOT NULL DEFAULT 0,
    missed INT NOT NULL DEFAULT 0
);
