-- « Lecture biblique » (LB) is counted in chapters and now also in time:
-- the members can time their reading with the stopwatch of the app.
UPDATE activity_types SET tracks_duration = TRUE WHERE user_id IS NULL AND code = 'LB';
