-- Run on the staging database, just after a copy of production is restored
-- (README.md): no personal data of the members stays. Kept as they are: the
-- owner (n° 1), the demo account @cmfic (n° 142) and its demo disciples
-- (numbers +00000000…), to sign in and to show.
BEGIN;

CREATE TEMP TABLE kept AS
SELECT id FROM users WHERE id IN (1, 142) OR phone_number LIKE '+00000000%';

-- the photos of the members: their keys, for the bucket of the staging
-- (README: they are deleted from it), then forgotten
SELECT m.key AS avatar_key
FROM users u JOIN media m ON m.id = u.avatar_media_id
WHERE u.id NOT IN (SELECT id FROM kept);

UPDATE users SET
  first_name = 'Membre',
  last_name = id::text,
  phone_number = '+0001' || lpad(id::text, 9, '0'),
  email = NULL,
  username = 'staging.m' || id,
  hash_password = decode(md5(random()::text), 'hex'),
  avatar_media_id = NULL
WHERE id NOT IN (SELECT id FROM kept);

UPDATE profiles SET
  first_name = 'Membre',
  last_name = user_id::text,
  phone_number = '+0001' || lpad(user_id::text, 9, '0'),
  email = NULL,
  birth_date = NULL
WHERE user_id NOT IN (SELECT id FROM kept);

UPDATE authorities SET
  first_name = 'Autorité',
  last_name = id::text,
  phone_number = NULL,
  email = NULL
WHERE user_id NOT IN (SELECT id FROM kept);

UPDATE daily_reports SET note = NULL
WHERE note IS NOT NULL AND user_id NOT IN (SELECT id FROM kept);

UPDATE report_sendings SET reply = '' WHERE reply <> '';
UPDATE prayer_people SET name = 'Personne ' || id, note = '';
UPDATE importunities SET title = 'Sujet ' || id, description = '';
UPDATE payments SET phone_number = '+0000000000';
UPDATE payouts SET phone_number = '+0000000000', holder = 'Bénéficiaire';

-- nothing that reaches a member, a phone or a store from here
DELETE FROM devices;
DELETE FROM premium_renewals;
DELETE FROM user_identities;
DELETE FROM member_reports;
DELETE FROM souls;

COMMIT;
