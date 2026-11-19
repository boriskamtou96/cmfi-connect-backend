#!/usr/bin/env bash
# A full copy of the database, every night (cron, see README.md):
#   0 3 * * * /opt/cmfi-connect-backend/deploy/vps/backup.sh >> /var/log/cmfi-backup.log 2>&1
#
# Keeps the last BACKUP_KEEP copies (14) in BACKUP_DIR (/var/backups/cmfi),
# and sends each one to BACKUP_RCLONE_REMOTE (.env) when it is set.
# Restore: README.md, « Restaurer une sauvegarde ».
set -euo pipefail

cd "$(dirname "$0")"

dir=${BACKUP_DIR:-/var/backups/cmfi}
keep=${BACKUP_KEEP:-14}
remote=$(grep -E '^BACKUP_RCLONE_REMOTE=' .env 2>/dev/null | cut -d= -f2- || true)

mkdir -p "$dir"
chmod 700 "$dir"

file="$dir/cmfi_connect-$(date -u +%Y-%m-%dT%H%MZ).dump"
docker compose exec -T postgres pg_dump -U cmfi -d cmfi_connect -Fc > "$file.part"
mv "$file.part" "$file"
echo "$(date -u +%FT%TZ) saved $file ($(du -h "$file" | cut -f1))"

# only the newest copies stay
ls -1t "$dir"/cmfi_connect-*.dump | tail -n +"$((keep + 1))" | xargs -r rm --

if [ -n "$remote" ]; then
  rclone copy "$file" "$remote"
  echo "$(date -u +%FT%TZ) sent to $remote"
fi
