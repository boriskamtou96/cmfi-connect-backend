# Le staging : l'environnement de test

Une seconde API et sa base, sur le même VPS que la production (`158.220.73.117`), pour essayer avant de mettre en production :

| | Production | Staging |
|---|---|---|
| Dépôt | `/opt/cmfi-connect-backend`, branche `main` | `/opt/cmfi-connect-staging`, branche `dev` |
| Adresse | `api.cmfi-connect.com` | `staging-api.cmfi-connect.com` |
| Base | la vraie | une copie anonymisée (`anonymize.sql`) |
| Firebase | `cmfi-connect` | `cmfi-connect-dev` : aucune notification n'atteint un membre |
| Images, sons | bucket R2 `cmfi-connect-media` | bucket `cmfi-connect-media-staging` (une copie) |
| Paiements | KPay et Wajub | KPay sandbox seulement |

Le Caddy de la production sert les deux. L'API du staging le rejoint par le réseau Docker `cmfi_edge`, où la base de production n'est pas : le staging ne peut pas l'atteindre.

## Le chemin d'une modification

```bash
# 1. sur le Mac : pousser sur dev
git push git@github.com:boriskamtou96/cmfi-connect-backend.git dev
# 2. le staging la prend, on essaie (app dev : make run-staging ; admin : dev.cmfi-connect-admin.pages.dev)
ssh root@158.220.73.117 'cd /opt/cmfi-connect-staging && git pull && cd deploy/staging && docker compose up -d --build api'
# 3. bon : dev passe dans main, puis la production
git push git@github.com:boriskamtou96/cmfi-connect-backend.git dev:main
ssh root@158.220.73.117 'cd /opt/cmfi-connect-backend && git pull && cd deploy/vps && docker compose up -d --build api'
```

`main` n'est jamais modifiée à part : elle avance à `dev` (`dev:main` refuse un historique qui a divergé).

## Installer (une fois)

```bash
docker network create cmfi_edge          # puis, côté production : docker compose up -d --force-recreate caddy
git clone -b dev git@github.com:boriskamtou96/cmfi-connect-backend.git /opt/cmfi-connect-staging
cd /opt/cmfi-connect-staging/deploy/staging
cp .env.example .env && chmod 600 .env   # le reste vient de ../../../cmfi-connect-backend/deploy/vps/.env
# la clé Firebase du projet cmfi-connect-dev, lisible par le conteneur seul (utilisateur 10001)
install -m 400 -o 10001 firebase-dev.json /etc/cmfi/firebase-staging.json
```

Chez Cloudflare : `staging-api` en `A` vers `158.220.73.117`, proxy désactivé ; le bucket `cmfi-connect-media-staging` avec son adresse publique (`MEDIA_PUBLIC_URL`) et un jeton R2 (`S3_*`) limité à ce bucket.

## Rafraîchir les données (une copie anonymisée de la production)

```bash
cd /opt/cmfi-connect-staging/deploy/staging
docker compose stop api
docker compose -f /opt/cmfi-connect-backend/deploy/vps/docker-compose.yml exec -T postgres \
  pg_dump -U cmfi -d cmfi_connect -Fc > /tmp/prod.dump
docker compose exec -T postgres dropdb -U cmfi cmfi_connect
docker compose exec -T postgres createdb -U cmfi cmfi_connect
docker compose exec -T postgres pg_restore -U cmfi -d cmfi_connect --no-owner --no-acl < /tmp/prod.dump
rm /tmp/prod.dump
docker compose exec -T postgres psql -U cmfi -d cmfi_connect -At -v ON_ERROR_STOP=1 < anonymize.sql > /dev/null
docker compose up -d api
```

Les fichiers : la copie du bucket de production, sans les photos des membres. Chaque bucket a son jeton (celui des `.env`), donc un accès rclone chacun, lu dans les `.env` (leurs valeurs sont entre apostrophes) :

```bash
get() { grep "^$2=" "$1" | cut -d= -f2- | tr -d "'\"" ; }
P=/opt/cmfi-connect-backend/deploy/vps/.env; S=/opt/cmfi-connect-staging/deploy/staging/.env
for r in PROD:$P STG:$S; do n=${r%%:*}; f=${r#*:}
  export RCLONE_CONFIG_${n}_TYPE=s3 RCLONE_CONFIG_${n}_PROVIDER=Cloudflare RCLONE_CONFIG_${n}_REGION=auto RCLONE_CONFIG_${n}_NO_CHECK_BUCKET=true
  export RCLONE_CONFIG_${n}_ENDPOINT="$(get $f S3_ENDPOINT)" RCLONE_CONFIG_${n}_ACCESS_KEY_ID="$(get $f S3_ACCESS_KEY_ID)" RCLONE_CONFIG_${n}_SECRET_ACCESS_KEY="$(get $f S3_SECRET_ACCESS_KEY)"
done
docker compose -f /opt/cmfi-connect-backend/deploy/vps/docker-compose.yml exec -T postgres psql -U cmfi -d cmfi_connect -At \
  -c "select m.key from users u join media m on m.id = u.avatar_media_id where u.id not in (1, 142)" > /tmp/avatars.txt
rclone copy prod:cmfi-connect-media stg:cmfi-connect-media-staging --transfers 16 --exclude-from /tmp/avatars.txt
rm /tmp/avatars.txt
```

La démo (un an de comptes rendus pour @cmfic et ses 5 disciples), qui n'existe que sur le staging, se recrée après un rafraîchissement :

```bash
docker compose exec -T postgres psql -U cmfi -d cmfi_connect -v ON_ERROR_STOP=1 -q < demo.sql
```

Se connecter au staging : avec le compte du propriétaire (n° 1) ou le compte de démonstration @cmfic, mêmes mots de passe qu'en production. Les autres membres sont devenus « Membre n° … », sans mot de passe utilisable.

## Mettre à jour

```bash
cd /opt/cmfi-connect-staging && git pull
cd deploy/staging && docker compose up -d --build api
```
