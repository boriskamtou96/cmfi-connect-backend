-- The demo of the staging (never in production): a year of reports for the
-- account @cmfic (n° 142) and its 5 disciples, with theirs. Run after a
-- refresh (README.md); a second run changes nothing (accounts kept, days
-- already filled skipped). The disciples have numbers no member can have
-- (+00000000…) and no usable password.
BEGIN;
SELECT setseed(0.2026);

CREATE TEMP TABLE seed_people (username text, first_name text, last_name text, phone text, regularity float, start_days int, assembly text);
INSERT INTO seed_people VALUES
 ('paul.ngono',    'Paul',   'Ngono',  '+0000000001', 0.92, 365, 'Assemblée de Biyem-Assi'),
 ('esther.mbarga', 'Esther', 'Mbarga', '+0000000002', 0.82, 365, 'Assemblée de Mvog-Ada'),
 ('daniel.tchoua', 'Daniel', 'Tchoua', '+0000000003', 0.68, 300, 'Assemblée d''Essos'),
 ('ruth.ewane',    'Ruth',   'Ewane',  '+0000000004', 0.50, 180, 'Assemblée de Biyem-Assi'),
 ('samuel.fotso',  'Samuel', 'Fotso',  '+0000000005', 0.35, 365, 'Assemblée de Nkolbisson');

INSERT INTO users (first_name, last_name, hash_password, phone_number, username, created_at, has_password)
SELECT first_name, last_name, decode(md5(random()::text) || md5(random()::text), 'hex'), phone, username,
       current_date - start_days - 2, true
FROM seed_people s WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.username = s.username);

INSERT INTO profiles (user_id, city, country, church, assembly, first_name, last_name, phone_number)
SELECT u.id, 'Yaoundé', 'CM', 'CMCI', s.assembly, s.first_name, s.last_name, s.phone
FROM users u JOIN seed_people s USING (username)
WHERE NOT EXISTS (SELECT 1 FROM profiles p WHERE p.user_id = u.id);

INSERT INTO authorities (user_id, first_name, last_name, is_disciple_maker, linked_user_id, status, shares_regularity, created_at)
SELECT u.id, 'CMFI', 'Connect', true, 142, 'accepted', true, u.created_at + interval '1 day'
FROM users u JOIN seed_people s USING (username)
WHERE NOT EXISTS (SELECT 1 FROM authorities a WHERE a.user_id = u.id AND a.linked_user_id = 142);

CREATE TEMP TABLE seed_members (user_id bigint, regularity float, start_days int);
INSERT INTO seed_members VALUES (142, 0.88, 365);
INSERT INTO seed_members SELECT u.id, s.regularity, s.start_days FROM users u JOIN seed_people s USING (username);

-- one draw per day
CREATE TEMP TABLE seed_days AS
SELECT user_id, day FROM (
  SELECT m.user_id, m.regularity, d::date AS day, random() AS draw
  FROM seed_members m, generate_series(current_date - m.start_days, current_date - 1, interval '1 day') d
) x WHERE draw < regularity;

CREATE TEMP TABLE seed_reports (id bigint, day date);
WITH made AS (
  INSERT INTO daily_reports (user_id, report_date, created_at, updated_at)
  SELECT user_id, day, day + time '20:30' + random() * interval '3 hours', day + time '20:30' + random() * interval '3 hours'
  FROM seed_days
  ON CONFLICT (user_id, report_date) DO NOTHING
  RETURNING id, report_date
)
INSERT INTO seed_reports SELECT id, report_date FROM made;

INSERT INTO report_entries (report_id, activity_type_id, quantity, duration_minutes)
SELECT id, 1, 1 + floor(random() * 4)::int, 10 + floor(random() * 30)::int FROM seed_reports WHERE random() < 0.9
UNION ALL SELECT id, 2, NULL, 15 + floor(random() * 75)::int FROM seed_reports WHERE random() < 0.95
UNION ALL SELECT id, 3, 1, 10 + floor(random() * 20)::int FROM seed_reports WHERE random() < 0.7
UNION ALL SELECT id, 4, 1, 45 + floor(random() * 75)::int FROM seed_reports WHERE extract(dow FROM day) = 0 OR random() < 0.15
UNION ALL SELECT id, 5, 5 + floor(random() * 35)::int, 15 + floor(random() * 45)::int FROM seed_reports WHERE random() < 0.45
UNION ALL SELECT id, 6, 1, NULL FROM seed_reports WHERE extract(dow FROM day) = 5 AND random() < 0.25
UNION ALL SELECT id, 7, 1, NULL FROM seed_reports WHERE random() < 0.08
UNION ALL SELECT id, 8, 10, NULL FROM seed_reports WHERE extract(dow FROM day) = 0 AND extract(day FROM day) <= 7
UNION ALL SELECT id, 9, 5 + floor(random() * 6)::int, NULL FROM seed_reports WHERE extract(dow FROM day) = 0 AND random() < 0.6;

COMMIT;
