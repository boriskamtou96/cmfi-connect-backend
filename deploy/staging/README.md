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
docker compose exec -T postgres psql -U cmfi -d cmfi_connect -At -v ON_ERROR_STOP=1 < anonymize.sql > /tmp/avatars.txt
docker compose up -d api
# les fichiers : la copie du bucket, sans les photos des membres
rclone sync r2:cmfi-connect-media r2:cmfi-connect-media-staging
grep -v '^\(BEGIN\|COMMIT\|SELECT\|UPDATE\|DELETE\)' /tmp/avatars.txt | while read -r key; do
  [ -n "$key" ] && rclone deletefile "r2:cmfi-connect-media-staging/$key"
done
rm /tmp/avatars.txt
```

Se connecter au staging : avec le compte du propriétaire (n° 1) ou le compte de démonstration @cmfic, mêmes mots de passe qu'en production. Les autres membres sont devenus « Membre n° … », sans mot de passe utilisable.

## Mettre à jour

```bash
cd /opt/cmfi-connect-staging && git pull
cd deploy/staging && docker compose up -d --build api
```
