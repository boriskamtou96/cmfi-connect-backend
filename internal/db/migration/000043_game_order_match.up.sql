-- Two more kinds of questions: « order » (put 4 to 6 things in order; the
-- answer is the ids in the right order, "c,a,d,b") and « match » (link 3 or
-- 4 pairs; the answer is "l1=r2,l2=r4,…", by left id). Such answers are
-- longer than a choice id.
ALTER TABLE game_questions DROP CONSTRAINT IF EXISTS game_questions_kind_check;
ALTER TABLE game_questions
    ADD CONSTRAINT game_questions_kind_check
    CHECK (kind IN ('choice', 'true_false', 'fill_verse', 'order', 'match'));
ALTER TABLE game_questions ALTER COLUMN answer TYPE VARCHAR(200);
