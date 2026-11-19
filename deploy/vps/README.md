# L'API et sa base sur notre VPS

Le serveur de production (`api.cmfi-connect.com`) sur un VPS Contabo (Cloud VPS 6, Ubuntu 24.04, `158.220.73.117`), avec Docker Compose :

- `postgres` : Postgres 18, données dans le volume `pgdata`, jamais ouvert sur Internet ;
- `api` : l'image du `Dockerfile` du dépôt (migrations au démarrage, comme sur Render) ;
- `caddy` : le HTTPS (Let's Encrypt) devant l'API, ports 80 et 443.

Les images restent sur Cloudflare R2 (`media.cmfi-connect.com`). Sauvegarde de la base chaque nuit (`backup.sh`).

Le dépôt est dans `/opt/cmfi-connect-backend` sur le serveur ; les commandes ci-dessous se lancent dans `/opt/cmfi-connect-backend/deploy/vps`.

## 1. Préparer le serveur (une fois)

```bash
ssh root@158.220.73.117

apt update && apt upgrade -y
apt install -y ca-certificates curl git ufw unattended-upgrades rclone
dpkg-reconfigure -f noninteractive unattended-upgrades   # mises à jour de sécurité seules

# Docker (dépôt officiel)
curl -fsSL https://get.docker.com | sh

# pare-feu : SSH, HTTP, HTTPS
ufw allow OpenSSH && ufw allow 80/tcp && ufw allow 443/tcp && ufw allow 443/udp
ufw --force enable

# SSH par clé seulement (après avoir vérifié que la clé marche !)
sed -i 's/^#\?PasswordAuthentication .*/PasswordAuthentication no/' /etc/ssh/sshd_config
systemctl reload ssh
```

Le dépôt, avec une clé de déploiement en lecture seule :

```bash
ssh-keygen -t ed25519 -N "" -f ~/.ssh/github_deploy -C "cmfi-vps"
cat ~/.ssh/github_deploy.pub   # GitHub › cmfi-connect-backend › Settings › Deploy keys (lecture seule)
cat >> ~/.ssh/config <<'EOF'
Host github.com
  IdentityFile ~/.ssh/github_deploy
EOF
git clone -b dev git@github.com:boriskamtou96/cmfi-connect-backend.git /opt/cmfi-connect-backend
```

## 2. Les réglages

```bash
cd /opt/cmfi-connect-backend/deploy/vps
cp .env.example .env && chmod 600 .env
openssl rand -hex 24          # → POSTGRES_PASSWORD
nano .env                     # le reste : les valeurs du service Render (onglet Environment)
```

`TOKEN_SECRET` doit être celui de Render : sinon tous les membres sont déconnectés.

La clé Firebase (le contenu de `FIREBASE_CREDENTIALS_JSON` sur Render) dans un fichier que seul le conteneur lit (utilisateur 10001) :

```bash
mkdir -p /etc/cmfi
nano /etc/cmfi/firebase-credentials.json
chown 10001 /etc/cmfi/firebase-credentials.json && chmod 400 /etc/cmfi/firebase-credentials.json
```

## 3. Démarrer et vérifier

```bash
docker compose up -d --build
docker compose ps
curl -s http://127.0.0.1:8080/v1/health
docker compose logs -f api
```

Avant que le DNS pointe ici, Caddy ne peut pas avoir de certificat (il réessaie seul). On essaie le HTTPS de l'extérieur une fois le DNS changé.

## 4. Copier la base de Neon (la bascule)

Pendant la copie, plus rien ne doit écrire dans Neon : on arrête l'API sur Render (Dashboard › Suspend), quelques minutes.

Sur le Mac (pg_dump 18), avec `NEON_DSN` :

```bash
pg_dump "$NEON_DSN" -Fc --no-owner --no-acl -f neon.dump
scp neon.dump root@158.220.73.117:/tmp/
```

Sur le serveur, dans une base vide (l'API arrêtée pour qu'elle n'y crée pas ses tables) :

```bash
docker compose stop api
docker compose exec -T postgres dropdb -U cmfi cmfi_connect
docker compose exec -T postgres createdb -U cmfi cmfi_connect
docker compose exec -T postgres pg_restore -U cmfi -d cmfi_connect --no-owner --no-acl < /tmp/neon.dump
docker compose up -d api
```

Vérifier que les nombres sont les mêmes des deux côtés (membres, comptes rendus, version des migrations) :

```bash
q="select (select count(*) from users), (select count(*) from daily_reports), (select version from schema_migrations)"
psql "$NEON_DSN" -Atc "$q"                                               # sur le Mac
docker compose exec -T postgres psql -U cmfi -d cmfi_connect -Atc "$q"    # sur le serveur
```

Puis, chez le gestionnaire du domaine `cmfi-connect.com` : l'enregistrement `api` en `A` vers `158.220.73.117` (et retirer l'ancien CNAME vers Render). Baisser le TTL à 300 s la veille aide. Une fois le DNS propagé :

```bash
curl -s https://api.cmfi-connect.com/v1/health
```

Puis se connecter dans l'app. Les webhooks KPay et Wajub suivent tout seuls : même adresse.

Retour en arrière : remettre le DNS vers Render et relancer le service Render. Ce qui a été écrit sur le VPS depuis la bascule n'est pas dans Neon.

Garder Render (suspendu) et Neon une semaine, puis les arrêter.

## 5. Sauvegardes

```bash
chmod +x backup.sh
./backup.sh                   # un essai
crontab -e                    # ajouter :
# 0 3 * * * /opt/cmfi-connect-backend/deploy/vps/backup.sh >> /var/log/cmfi-backup.log 2>&1
```

Copies dans `/var/backups/cmfi` (les 14 dernières). Pour en garder une hors du serveur, sur R2 : `rclone config` (type `s3`, fournisseur Cloudflare, les clés d'un jeton R2 limité au bucket des sauvegardes), puis `BACKUP_RCLONE_REMOTE=r2:cmfi-backups/db` dans `.env`.

### Restaurer une sauvegarde

```bash
docker compose stop api
docker compose exec -T postgres dropdb -U cmfi cmfi_connect
docker compose exec -T postgres createdb -U cmfi cmfi_connect
docker compose exec -T postgres pg_restore -U cmfi -d cmfi_connect --no-owner < /var/backups/cmfi/cmfi_connect-<date>.dump
docker compose up -d api
```

## Mettre à jour le serveur

```bash
cd /opt/cmfi-connect-backend && git pull
cd deploy/vps && docker compose up -d --build api
```

Les commandes du dépôt sont dans l'image : `docker compose exec api import-hymns data/hymns/<recueil>.json`, `docker compose exec api promote <numéro>`.
