# Règles de travail sur cmfi-connect

## Aucune modification sans mon accord

Ne crée, ne modifie, ne renomme et ne supprime aucun fichier de ce projet sans mon accord explicite.

- Avant d'écrire quoi que ce soit, présente ce que tu comptes faire : les fichiers concernés et la nature du changement, avec un extrait du diff pour les modifications importantes. Attends un « oui » clair.
- Un accord ne vaut que pour ce qui a été présenté. Un problème découvert en chemin (bug, faille, code généré à mettre à jour) se signale et attend un nouvel accord : il ne se corrige pas d'office.
- La règle couvre aussi les commandes qui modifient le projet ou sa base de développement : `sqlc generate`, `go mod tidy`, `gofmt -w`, les migrations, les tests qui écrivent dans la base `cmfi_connect`, les commits.
- Lire, chercher, compiler sans écrire de fichier, et tester sur une base jetable restent libres.
