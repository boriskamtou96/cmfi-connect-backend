# syntax=docker/dockerfile:1

# The API for production (Render, Railway, Fly.io…): a small image that applies
# the pending migrations, then starts the server. See docker/entrypoint.sh for
# the settings it reads.

# ----- build: the server, its command-line tools, and migrate -----
FROM golang:1.26-alpine AS build
WORKDIR /src
# static binaries: the final image has no C library to offer them
ENV CGO_ENABLED=0

# the tool of the migrations, with Postgres only (the version used in dev)
RUN go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/server . \
 && go build -trimpath -ldflags="-s -w" -o /out/import-hymns ./cmd/import-hymns \
 && go build -trimpath -ldflags="-s -w" -o /out/promote ./cmd/promote \
 && go build -trimpath -ldflags="-s -w" -o /out/media-to-s3 ./cmd/media-to-s3

# ----- run -----
FROM alpine:3.22

# certificates (Firebase, R2), time zones (the events), and a user without rights
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 app

WORKDIR /app
COPY --from=build /go/bin/migrate /out/ /usr/local/bin/
COPY internal/db/migration ./migrations
# the hymn books, for `import-hymns data/hymns/*.json`
COPY data/hymns ./data/hymns
COPY --chmod=755 docker/entrypoint.sh /usr/local/bin/entrypoint.sh

# images on disk only without bucket (S3_BUCKET): mount a volume there then
RUN mkdir -p /app/data/media && chown app /app/data/media

ENV GIN_MODE=release \
    LOG_LEVEL=info \
    PORT=8080 \
    MEDIA_DIR=/app/data/media \
    MIGRATIONS_PATH=/app/migrations

USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=30s \
  CMD wget -qO- "http://127.0.0.1:${PORT}/v1/health" > /dev/null || exit 1

ENTRYPOINT ["entrypoint.sh"]
CMD ["server"]
