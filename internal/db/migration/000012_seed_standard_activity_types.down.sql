DELETE FROM activity_types
WHERE user_id IS NULL
  AND code IN ('LB', 'PS', 'RDQAD', 'PG', 'LLC', 'JC', 'JP', 'Dîmes', 'OFF');