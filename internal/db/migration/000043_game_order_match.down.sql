DELETE FROM game_questions WHERE kind IN ('order', 'match');
ALTER TABLE game_questions ALTER COLUMN answer TYPE VARCHAR(20);
ALTER TABLE game_questions DROP CONSTRAINT IF EXISTS game_questions_kind_check;
ALTER TABLE game_questions
    ADD CONSTRAINT game_questions_kind_check
    CHECK (kind IN ('choice', 'true_false', 'fill_verse'));
