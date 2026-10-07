include .env
MIGRATIONS_PATH = ./internal/db/migration
COMPOSE_FILE = ./docker-compose.yml
DB_ADDR = postgresql://postgres:postgres@localhost:5432/cmfi_connect?sslmode=disable

.PHONY: migrate-create
migration:
	@migrate create -seq -ext sql -dir ${MIGRATIONS_PATH} $(filter-out $@,$(MAKECMDGOALS))

.PHONY: migrate-up
migrate-up:
	@migrate -path=${MIGRATIONS_PATH} -database=$(DB_ADDR) up


.PHONY: migrate-down
migrate-down:
	@migrate -path=${MIGRATIONS_PATH} -database=$(DB_ADDR) down $(filter-out $@,$(MAKECMDGOALS))


.PHONY: docker-up
docker-up:
	docker compose -f ${COMPOSE_FILE} up -d


.PHONY: docker-down
docker-down:
	docker compose -f ${COMPOSE_FILE} down

.PHONY: test
test:
	go test -v -cover ./...

.PHONY: run
run:
	go run ./main.go

.PHONY: sqlc
sqlc:
	sqlc generate